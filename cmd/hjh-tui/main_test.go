package main

import (
	"io"
	"testing"
)

func TestOptionalRunValuesPreserveAbsentAndExplicitZero(t *testing.T) {
	_, absent, err := parseOptions([]string{"--endpoint", "http://127.0.0.1:7483"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if absent.MaxSteps != nil || absent.FromTurn != nil {
		t.Fatalf("omitted options became explicit values: maxSteps=%v fromTurn=%v", absent.MaxSteps, absent.FromTurn)
	}
	_, zero, err := parseOptions([]string{"--endpoint", "http://127.0.0.1:7483", "--max-steps=0", "--from-turn=0"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if zero.MaxSteps == nil || *zero.MaxSteps != 0 || zero.FromTurn == nil || *zero.FromTurn != 0 {
		t.Fatalf("explicit zero was not retained: maxSteps=%v fromTurn=%v", zero.MaxSteps, zero.FromTurn)
	}
}
