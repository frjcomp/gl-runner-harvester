package main

import "testing"

func TestMainCallsExecute(t *testing.T) {
	called := false
	old := execute
	execute = func() { called = true }
	defer func() { execute = old }()

	main()

	if !called {
		t.Fatalf("expected execute to be called")
	}
}
