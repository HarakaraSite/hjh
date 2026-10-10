package main

import "testing"

func TestNormalScreenStreamingSeparation(t *testing.T) {
	if err := checkNormalScreenAndStreamSeparation(); err != nil {
		t.Fatal(err)
	}
}

func TestJapaneseTextareaResizeAndSubmitKeys(t *testing.T) {
	if err := checkJapaneseEditorResizeAndNewlines(); err != nil {
		t.Fatal(err)
	}
}
