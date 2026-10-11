package main

import (
	"bytes"
	"strings"
	"testing"

	"hjh/internal/tui/renderer"
)

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

func TestIndependentWriterDemoBuildsWholeMutableFrame(t *testing.T) {
	var output bytes.Buffer
	m := &writerDemoModel{writer: renderer.New(&output), columns: 80, rows: 24, stablePending: true}
	if err := m.render(); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"S06 independent Go writer", "assistant: Q", "live update: 0"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("independent writer output missing %q: %q", want, output.String())
		}
	}
}

func TestSourceReceiptKeepsScrolledPrefixOutOfMutableTail(t *testing.T) {
	text := probeText(4)
	if got := strings.Count(strings.Join(renderer.WrapText(text, 80), "\n"), probeToken); got != probeTokenCount {
		t.Fatalf("wrapping changed source token count: got %d want %d", got, probeTokenCount)
	}
	offset := suffixOffsetByCells(text, 80*20)
	if offset == 0 || offset >= len(text) {
		t.Fatalf("80-column live tail did not split the long stream: offset=%d length=%d", offset, len(text))
	}
	widePrefix, wideTail := text[:offset], text[offset:]
	if strings.Count(widePrefix+wideTail, probeToken) != probeTokenCount {
		t.Fatalf("80-column receipt lost source text: prefix/tail=%q%q", widePrefix, wideTail)
	}
	narrowOffset := suffixOffsetByCells(text, 44*20)
	if narrowOffset <= offset {
		t.Fatalf("narrow screen did not advance the receipt: wide=%d narrow=%d", offset, narrowOffset)
	}
	if strings.Count(text[:narrowOffset]+text[narrowOffset:], probeToken) != probeTokenCount {
		t.Fatalf("44-column receipt lost source text at offset %d", narrowOffset)
	}
	if suffixOffsetByCells(text, 80*20) > narrowOffset {
		t.Fatal("expanding the terminal moved the source receipt backward")
	}
}
