// SPDX-License-Identifier: Apache-2.0

package listcmd_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
	"github.com/wilfriedroset/a10r/internal/config"
	"github.com/wilfriedroset/a10r/internal/listcmd"
	"github.com/wilfriedroset/a10r/internal/output"
)

// fakeRow is the smallest possible row type — the pipeline is
// generic over R so the test type only carries the fields the
// fanOut + render assertions touch.
type fakeRow struct {
	Backend string
	Value   string
}

// fakeClient is a sentinel backend.Client the test fetcher reads to
// confirm the pipeline routes the matching builder output. The full
// interface surface is implemented as no-ops so the test does not
// have to grow a method for every backend.Client capability.
type fakeClient struct {
	backend.Client
	name string
}

func newSpec(t *testing.T, backends []config.Backend, fetcher listcmd.Fetcher[fakeRow]) listcmd.Spec[fakeRow] {
	t.Helper()
	cfg := &config.Config{Backends: backends}
	return listcmd.Spec[fakeRow]{
		Config:  cfg,
		Format:  output.FormatJSON,
		Fetcher: fetcher,
		Renderers: map[output.Format]listcmd.Renderer[fakeRow]{
			output.FormatJSON: func(w io.Writer, rows []fakeRow) error {
				for _, r := range rows {
					_, _ = fmt.Fprintf(w, "%s=%s\n", r.Backend, r.Value)
				}
				return nil
			},
			output.FormatTable: func(w io.Writer, rows []fakeRow) error {
				_, _ = fmt.Fprintf(w, "TABLE: %d rows\n", len(rows))
				return nil
			},
		},
		Sort: func(rows []fakeRow) {
			sort.Slice(rows, func(i, j int) bool {
				if rows[i].Backend != rows[j].Backend {
					return rows[i].Backend < rows[j].Backend
				}
				return rows[i].Value < rows[j].Value
			})
		},
		ResourceLabel: "thing",
		Out:           &bytes.Buffer{},
		Deps: listcmd.Deps{
			BuildClient: func(be config.Backend) (backend.Client, error) {
				return fakeClient{name: be.Name}, nil
			},
		},
	}
}

func TestRun_HappyPath_FansOutAndRenders(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "alpha"}, {Name: "beta"}},
		func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
			return []fakeRow{{Backend: name, Value: "one"}, {Backend: name, Value: "two"}}, nil
		},
	)
	spec.Out = &out

	require.NoError(t, listcmd.Run(context.Background(), spec))
	rendered := out.String()
	// Sort must have run: rows print in backend then value order.
	require.Equal(t, "alpha=one\nalpha=two\nbeta=one\nbeta=two\n", rendered)
}

func TestRun_PartialFailure_RoutesErrToStderrAndKeepsRows(t *testing.T) {
	t.Parallel()

	var out, errBuf bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "good"}, {Name: "bad"}},
		func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
			if name == "bad" {
				return nil, errors.New("boom")
			}
			return []fakeRow{{Backend: name, Value: "v"}}, nil
		},
	)
	spec.Out = &out
	spec.Deps.Stderr = &errBuf

	require.NoError(t, listcmd.Run(context.Background(), spec))
	require.Contains(t, errBuf.String(), `backend "bad": list: boom`)
	require.Equal(t, "good=v\n", out.String())
}

func TestRun_AllBackendsFailed_ReturnsCanonicalError(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "down1"}, {Name: "down2"}},
		func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
			return nil, fmt.Errorf("%s unreachable", name)
		},
	)
	spec.Out = &bytes.Buffer{}
	spec.Deps.Stderr = &errBuf
	spec.ResourceLabel = "alert"

	err := listcmd.Run(context.Background(), spec)
	require.Error(t, err)
	require.ErrorIs(t, err, listcmd.ErrAllBackendsFailed)
	require.Contains(t, err.Error(), "every configured backend failed to list alerts")
	// Stderr lines are sorted alphabetically by backend name for
	// reproducibility across the parallel fan-out.
	lines := strings.Split(strings.TrimSpace(errBuf.String()), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `backend "down1"`)
	require.Contains(t, lines[1], `backend "down2"`)
}

func TestRun_StderrSorted_EvenWhenFetcherCompletesOutOfOrder(t *testing.T) {
	t.Parallel()

	// Gate the fakes so "zeta" completes BEFORE "alpha": alpha
	// blocks until zeta has returned its error. The sort is then
	// the only thing that can place alpha-then-zeta on stderr — if
	// the sort were missing, output would be in completion order
	// (zeta first) and the assertion would fail.
	zetaSent := make(chan struct{})
	var errBuf bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "zeta"}, {Name: "alpha"}},
		func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
			if name == "zeta" {
				close(zetaSent)
				return nil, errors.New("zeta-err")
			}
			<-zetaSent
			return nil, errors.New("alpha-err")
		},
	)
	spec.Out = &bytes.Buffer{}
	spec.Deps.Stderr = &errBuf

	err := listcmd.Run(context.Background(), spec)
	require.ErrorIs(t, err, listcmd.ErrAllBackendsFailed)
	lines := strings.Split(strings.TrimSpace(errBuf.String()), "\n")
	require.Len(t, lines, 2)
	require.Contains(t, lines[0], `backend "alpha"`, "alphabetical name comes first")
	require.Contains(t, lines[1], `backend "zeta"`)
}

func TestRun_FailOnAny_WrapsErrMatchedWithCountAndLabel(t *testing.T) {
	t.Parallel()

	spec := newSpec(t, []config.Backend{{Name: "n"}},
		func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
			return []fakeRow{{Backend: name, Value: "v1"}, {Backend: name, Value: "v2"}}, nil
		},
	)
	spec.Out = &bytes.Buffer{}
	spec.FailOnAny = true
	spec.ResourceLabel = "silence"

	err := listcmd.Run(context.Background(), spec)
	require.Error(t, err)
	require.ErrorIs(t, err, listcmd.ErrMatched)
	require.Contains(t, err.Error(), "--fail: 2 silence(s) matched the filter")
}

func TestRun_FailOnAny_NoRowsSucceeds(t *testing.T) {
	t.Parallel()

	spec := newSpec(t, []config.Backend{{Name: "n"}},
		func(_ context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
			return nil, nil
		},
	)
	spec.Out = &bytes.Buffer{}
	spec.FailOnAny = true

	require.NoError(t, listcmd.Run(context.Background(), spec))
}

func TestRun_BuildClientFailure_CountsAsBackendFailure(t *testing.T) {
	t.Parallel()

	var errBuf bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "broken"}}, func(_ context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
		t.Fatal("fetcher must not run when BuildClient fails")
		return nil, nil
	})
	spec.Out = &bytes.Buffer{}
	spec.Deps.Stderr = &errBuf
	spec.Deps.BuildClient = func(_ config.Backend) (backend.Client, error) {
		return nil, errors.New("dial: refused")
	}

	err := listcmd.Run(context.Background(), spec)
	require.ErrorIs(t, err, listcmd.ErrAllBackendsFailed)
	require.Contains(t, errBuf.String(), `backend "broken": build: dial: refused`)
}

func TestRun_EmptyConfig_ReturnsNilAndRendersEmpty(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer
	spec := newSpec(t, nil, func(_ context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
		t.Fatal("fetcher must not run with no backends configured")
		return nil, nil
	})
	spec.Out = &out

	require.NoError(t, listcmd.Run(context.Background(), spec))
	// JSON renderer wrote nothing for an empty slice, no error.
	require.Empty(t, out.String())
}

func TestRun_FormatResolveToTable_OnTerminalDefault(t *testing.T) {
	t.Parallel()

	// Out is a bytes.Buffer (non-TTY), so the resolver picks json
	// when Format is empty. Verify by counting the body shape — the
	// table renderer prints "TABLE: ...", the json renderer prints
	// the row=value lines.
	var out bytes.Buffer
	spec := newSpec(t, []config.Backend{{Name: "x"}}, func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
		return []fakeRow{{Backend: name, Value: "v"}}, nil
	})
	spec.Format = ""
	spec.Out = &out

	require.NoError(t, listcmd.Run(context.Background(), spec))
	require.Equal(t, "x=v\n", out.String(), "non-TTY default is json")
}

func TestRun_UnknownFormat_ReturnsPreciseError(t *testing.T) {
	t.Parallel()

	spec := newSpec(t, []config.Backend{{Name: "n"}}, func(_ context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
		return nil, nil
	})
	spec.Format = output.Format("bogus")
	spec.Out = &bytes.Buffer{}

	err := listcmd.Run(context.Background(), spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no renderer for format")
}

func TestRun_RendererError_Propagates(t *testing.T) {
	t.Parallel()

	spec := newSpec(t, []config.Backend{{Name: "n"}}, func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
		return []fakeRow{{Backend: name}}, nil
	})
	want := errors.New("render boom")
	spec.Renderers[output.FormatJSON] = func(_ io.Writer, _ []fakeRow) error { return want }
	spec.Out = &bytes.Buffer{}

	err := listcmd.Run(context.Background(), spec)
	require.ErrorIs(t, err, want)
}

func TestRun_PagerFactoryInjected_LifecycleHonoured(t *testing.T) {
	t.Parallel()

	var opened, closed int
	pager := &trackingPager{onClose: func() { closed++ }}
	factory := func(_ context.Context, _ io.Writer, _, _ bool) (io.WriteCloser, error) {
		opened++
		return pager, nil
	}
	spec := newSpec(t, []config.Backend{{Name: "n"}}, func(_ context.Context, name string, _ backend.Client) ([]fakeRow, error) {
		return []fakeRow{{Backend: name, Value: "v"}}, nil
	})
	spec.Out = &bytes.Buffer{}
	spec.Deps.PagerFactory = factory

	require.NoError(t, listcmd.Run(context.Background(), spec))
	require.Equal(t, 1, opened, "pager opened exactly once")
	require.Equal(t, 1, closed, "pager closed exactly once")
	require.Equal(t, "n=v\n", pager.buf.String())
}

func TestRun_PagerFactoryError_ReturnsAsIs(t *testing.T) {
	t.Parallel()

	want := errors.New("less missing")
	spec := newSpec(t, []config.Backend{{Name: "n"}}, func(_ context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
		return nil, nil
	})
	spec.Out = &bytes.Buffer{}
	spec.Deps.PagerFactory = func(_ context.Context, _ io.Writer, _, _ bool) (io.WriteCloser, error) {
		return nil, want
	}

	err := listcmd.Run(context.Background(), spec)
	require.ErrorIs(t, err, want)
}

func TestRun_RequiresBuildClient(t *testing.T) {
	t.Parallel()

	spec := listcmd.Spec[fakeRow]{
		Config: &config.Config{Backends: []config.Backend{{Name: "n"}}},
		Format: output.FormatJSON,
		Out:    &bytes.Buffer{},
	}
	err := listcmd.Run(context.Background(), spec)
	require.Error(t, err)
	require.Contains(t, err.Error(), "BuildClient is required")
}

func TestRun_ContextCancelled_ReturnsCtxErr(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	spec := newSpec(t, []config.Backend{{Name: "n"}}, func(ctx context.Context, _ string, _ backend.Client) ([]fakeRow, error) {
		return nil, ctx.Err()
	})
	spec.Out = &bytes.Buffer{}

	err := listcmd.Run(ctx, spec)
	require.ErrorIs(t, err, context.Canceled)
}

// trackingPager is the fake io.WriteCloser the pager-lifecycle tests
// observe. Recording opens + closes asserts the Run body closes the
// pager exactly once even on the success path.
type trackingPager struct {
	buf     bytes.Buffer
	onClose func()
}

func (p *trackingPager) Write(b []byte) (int, error) { return p.buf.Write(b) }
func (p *trackingPager) Close() error {
	if p.onClose != nil {
		p.onClose()
	}
	return nil
}
