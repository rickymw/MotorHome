package main

import (
	"fmt"
	"os"
	"testing"
)

func TestCaptureStdout_TeesAndBuffers(t *testing.T) {
	finish, err := captureStdout()
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	fmt.Println("hello")
	fmt.Print("world\n")
	got := finish()
	want := "hello\nworld\n"
	if got != want {
		t.Errorf("captureStdout: got %q, want %q", got, want)
	}
}

func TestCaptureStdout_RestoresStdout(t *testing.T) {
	orig := os.Stdout
	finish, err := captureStdout()
	if err != nil {
		t.Fatalf("captureStdout: %v", err)
	}
	if os.Stdout == orig {
		t.Fatal("captureStdout: os.Stdout was not replaced")
	}
	_ = finish()
	if os.Stdout != orig {
		t.Error("captureStdout: finish did not restore os.Stdout")
	}
}
