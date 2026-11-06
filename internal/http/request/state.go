package request

import (
	"context"
	"net/http"
)

type State struct {
	Err      error
	WriteErr error
}

type stateKey struct{}

func WithState(r *http.Request) (*http.Request, *State) {
	if state, ok := r.Context().Value(stateKey{}).(*State); ok {
		return r, state
	}
	state := &State{}
	return r.WithContext(context.WithValue(r.Context(), stateKey{}, state)), state
}

func SetError(r *http.Request, err error) {
	if state, ok := r.Context().Value(stateKey{}).(*State); ok {
		state.Err = err
	}
}

func Error(r *http.Request) error {
	if state, ok := r.Context().Value(stateKey{}).(*State); ok {
		return state.Err
	}
	return nil
}

func SetWriteError(r *http.Request, err error) {
	if state, ok := r.Context().Value(stateKey{}).(*State); ok {
		state.WriteErr = err
	}
}

func WriteError(r *http.Request) error {
	if state, ok := r.Context().Value(stateKey{}).(*State); ok {
		return state.WriteErr
	}
	return nil
}
