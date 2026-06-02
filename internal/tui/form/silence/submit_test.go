// SPDX-License-Identifier: Apache-2.0

package silence

import (
	"context"
	"errors"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/stretchr/testify/require"

	"github.com/wilfriedroset/a10r/internal/backend"
)

func TestSubmitter_StartCreate(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantID: "sil-1"}
	var s submitter
	cmd := s.Start(client, "", backend.SilenceSpec{CreatedBy: "alice"})
	require.NotNil(t, cmd, "Start must return a Cmd that drives the write")
	require.True(t, s.InFlight(), "InFlight must be true between Start and Done")
	msg := cmd()
	done, ok := msg.(submitDoneMsg)
	require.True(t, ok, "Cmd must produce a submitDoneMsg, got %T", msg)
	require.Equal(t, "sil-1", done.id)
	require.False(t, done.updated, "create path must not flag the message as updated")
	require.NoError(t, done.err)
	require.Equal(t, 1, client.createCalls)
	require.Equal(t, 0, client.updateCalls)
}

func TestSubmitter_StartUpdate(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	var s submitter
	cmd := s.Start(client, "sil-7", backend.SilenceSpec{CreatedBy: "alice"})
	require.NotNil(t, cmd)
	done := cmd().(submitDoneMsg)
	require.Equal(t, "sil-7", done.id, "update path echoes the EditID")
	require.True(t, done.updated, "update path must flag the message as updated")
	require.Equal(t, 0, client.createCalls)
	require.Equal(t, 1, client.updateCalls)
	require.Equal(t, "sil-7", client.lastUpdateID)
}

func TestSubmitter_DoubleStartDrops(t *testing.T) {
	t.Parallel()
	client := &blockingClient{
		gate:    make(chan struct{}),
		started: make(chan struct{}),
	}
	var s submitter
	cmd1 := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd1, "first Start must return a Cmd")
	go func() { _ = cmd1() }()
	<-client.started
	require.True(t, s.InFlight(), "InFlight must be set while the first write is queued")

	cmd2 := s.Start(client, "", backend.SilenceSpec{})
	require.Nil(t, cmd2, "second Start during in-flight submit must return nil so caller surfaces a flash")

	close(client.gate)
}

func TestSubmitter_CancelAbortsInflight(t *testing.T) {
	t.Parallel()
	client := &ctxBlockingClient{started: make(chan struct{})}
	var s submitter
	cmd := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd)

	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSilence never started")
	}

	s.Cancel()

	select {
	case msg := <-done:
		got, ok := msg.(submitDoneMsg)
		require.True(t, ok, "expected submitDoneMsg after Cancel, got %T", msg)
		require.Error(t, got.err, "ctx-cancel must surface as an error to the caller's Done branch")
	case <-time.After(2 * time.Second):
		t.Fatal("Cancel did not abort the in-flight submit — goroutine leak window")
	}
}

func TestSubmitter_CancelWithoutInflightIsNoOp(t *testing.T) {
	t.Parallel()
	var s submitter
	require.NotPanics(t, s.Cancel, "Cancel must be safe to call when nothing is in flight")
}

func TestSubmitter_ParentCtxCancellationPropagates(t *testing.T) {
	t.Parallel()
	parent, cancelParent := context.WithCancel(context.Background())
	defer cancelParent()
	client := &ctxBlockingClient{started: make(chan struct{})}
	s := submitter{parent: parent}
	cmd := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()

	select {
	case <-client.started:
	case <-time.After(time.Second):
		t.Fatal("CreateSilence never started")
	}

	cancelParent()

	select {
	case msg := <-done:
		got, ok := msg.(submitDoneMsg)
		require.True(t, ok, "expected submitDoneMsg after parent cancel, got %T", msg)
		require.Error(t, got.err)
	case <-time.After(2 * time.Second):
		t.Fatal("parent ctx cancel did not propagate to the in-flight submit")
	}
}

func TestSubmitter_DoneAcceptsCurrentGen(t *testing.T) {
	t.Parallel()
	var s submitter
	s.inFlight = true
	s.gen = 1
	stale := s.Done(submitDoneMsg{gen: 1})
	require.False(t, stale, "Done with current gen must not be stale")
	require.False(t, s.InFlight(), "Done must clear the InFlight flag")
}

func TestSubmitter_DoneDropsStaleGen(t *testing.T) {
	t.Parallel()
	var s submitter
	s.inFlight = true
	s.gen = 1
	stale := s.Done(submitDoneMsg{gen: 99, id: "sil-stale"})
	require.True(t, stale, "Done with mismatched gen must report stale")
	require.True(t, s.InFlight(), "stale Done must NOT clear the InFlight flag — a current submit may still be live")
}

func TestSubmitter_StartBumpsGen(t *testing.T) {
	t.Parallel()
	client := &fakeClient{}
	var s submitter
	cmd1 := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd1)
	done1 := cmd1().(submitDoneMsg)
	require.Equal(t, 1, done1.gen, "first Start tags the message with gen=1")
	stale := s.Done(done1)
	require.False(t, stale)

	cmd2 := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd2)
	done2 := cmd2().(submitDoneMsg)
	require.Equal(t, 2, done2.gen, "second Start bumps gen to 2 so a stale done from #1 is rejected")
}

func TestSubmitter_DoneSurfacesClientError(t *testing.T) {
	t.Parallel()
	client := &fakeClient{wantErr: errors.New("boom")}
	var s submitter
	cmd := s.Start(client, "", backend.SilenceSpec{})
	require.NotNil(t, cmd)
	done := cmd().(submitDoneMsg)
	require.EqualError(t, done.err, "boom", "Done must carry the client error verbatim")
}
