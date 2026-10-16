package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteArchiveNormalizesMetadataAndPreservesFiles(t *testing.T) {
	base := t.TempDir()
	firstApp := filepath.Join(base, "first", "hjh-linux-amd64")
	secondApp := filepath.Join(base, "second", "hjh-linux-amd64")
	for _, app := range []string{firstApp, secondApp} {
		if err := os.MkdirAll(filepath.Join(app, "extensions"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "hjh"), []byte("core binary"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(app, "extensions", "hooks.js"), []byte("export {};\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chtimes(firstApp, time.Unix(100, 0), time.Unix(100, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(firstApp, "hjh"), time.Unix(200, 0), time.Unix(200, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(secondApp, time.Unix(900, 0), time.Unix(900, 0)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(secondApp, "hjh"), time.Unix(800, 0), time.Unix(800, 0)); err != nil {
		t.Fatal(err)
	}

	firstArchive := filepath.Join(base, "first.tar.gz")
	secondArchive := filepath.Join(base, "second.tar.gz")
	if err := writeArchive(firstApp, firstArchive); err != nil {
		t.Fatal(err)
	}
	if err := writeArchive(secondApp, secondArchive); err != nil {
		t.Fatal(err)
	}
	_, firstDigest, err := fileDigest(firstArchive)
	if err != nil {
		t.Fatal(err)
	}
	_, secondDigest, err := fileDigest(secondArchive)
	if err != nil {
		t.Fatal(err)
	}
	if firstDigest != secondDigest {
		t.Fatalf("archives differ after changing source mtimes: %s != %s", firstDigest, secondDigest)
	}

	archive, err := os.Open(firstArchive)
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Close()
	var gzipHeader [10]byte
	if _, err := io.ReadFull(archive, gzipHeader[:]); err != nil {
		t.Fatal(err)
	}
	if got := binary.LittleEndian.Uint32(gzipHeader[4:8]); got != 0 {
		t.Errorf("gzip mtime field = %d, want zero", got)
	}
	if _, err := archive.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	compressed, err := gzip.NewReader(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer compressed.Close()
	epoch := time.Unix(0, 0).UTC()
	if !compressed.ModTime.IsZero() {
		t.Fatalf("gzip mtime = %v, want an encoded zero timestamp", compressed.ModTime)
	}
	reader := tar.NewReader(compressed)
	contents := make(map[string]string)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Uid != 0 || header.Gid != 0 || !header.ModTime.Equal(epoch) {
			t.Errorf("archive metadata for %s = uid %d gid %d mtime %v", header.Name, header.Uid, header.Gid, header.ModTime)
		}
		if header.Typeflag == tar.TypeReg || header.Typeflag == tar.TypeRegA {
			data, err := io.ReadAll(reader)
			if err != nil {
				t.Fatal(err)
			}
			contents[header.Name] = string(data)
		}
	}
	if got := contents["hjh-linux-amd64/hjh"]; got != "core binary" {
		t.Errorf("archived core binary = %q", got)
	}
	if got := contents["hjh-linux-amd64/extensions/hooks.js"]; got != "export {};\n" {
		t.Errorf("archived editable extension = %q", got)
	}
}
