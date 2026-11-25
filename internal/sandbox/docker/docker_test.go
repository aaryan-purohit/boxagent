package docker

import (
	"archive/tar"
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestCapture(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		writes []string
		want   string
	}{
		{"nothing", nil, ""},
		{"under the limit", []string{"ab", "c"}, "abc"},
		{"at the limit in one write", []string{"abcdefgh"}, "abcdefgh"},
		{"at the limit in many writes", strings.Split("abcdefgh", ""), "abcdefgh"},
		{"over the limit in one write", []string{"abcdefghij"}, "abcd\n[discarded 2 bytes]\nghij"},
		{"over the limit in many writes", strings.Split("abcdefghijklmnopqrst", ""), "abcd\n[discarded 12 bytes]\nqrst"},
		{"over the limit in uneven writes", []string{"abc", "defghijk", "lm"}, "abcd\n[discarded 5 bytes]\njklm"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := capture{limit: 8}
			for _, w := range tc.writes {
				if n, err := c.Write([]byte(w)); n != len(w) || err != nil {
					t.Fatalf("Write(%q) = %d, %v", w, n, err)
				}
			}

			if got := c.String(); got != tc.want {
				t.Errorf("String() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestWithTimeoutRoundsUp(t *testing.T) {
	t.Parallel()
	got := withTimeout("ls -l", 1500*time.Millisecond)

	want := []string{"timeout", "-s", "KILL", "2", "sh", "-c", "ls -l"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("withTimeout() mismatch (-want +got):\n%s", diff)
	}
}

func TestFileArchive(t *testing.T) {
	t.Parallel()
	modTime := time.Unix(1700000000, 0)
	archive, err := fileArchive("a/b/c.txt", []byte("hi"), modTime)
	if err != nil {
		t.Fatalf("fileArchive() error: %v", err)
	}

	var names []string
	tr := tar.NewReader(archive)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("read archive: %v", err)
		}
		names = append(names, hdr.Name)
		if !hdr.ModTime.Equal(modTime) {
			t.Errorf("%s modified at %v, want %v", hdr.Name, hdr.ModTime, modTime)
		}
		if hdr.Uid != 1000 || hdr.Gid != 1000 {
			t.Errorf("%s is owned by %d:%d, want 1000:1000", hdr.Name, hdr.Uid, hdr.Gid)
		}
	}
	if diff := cmp.Diff([]string{"a/", "a/b/", "a/b/c.txt"}, names); diff != "" {
		t.Errorf("archive mismatch (-want +got):\n%s", diff)
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	archive := func(hdr tar.Header, content string) io.Reader {
		var buf bytes.Buffer
		tw := tar.NewWriter(&buf)
		hdr.Name, hdr.Size = "f", int64(len(content))
		if err := tw.WriteHeader(&hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
		return &buf
	}
	tests := []struct {
		name    string
		archive io.Reader
		want    string
		wantErr error
	}{
		{"reads a file at the limit", archive(tar.Header{Typeflag: tar.TypeReg}, "1234"), "1234", nil},
		{"reads an empty file", archive(tar.Header{Typeflag: tar.TypeReg}, ""), "", nil},
		{"rejects a file over the limit", archive(tar.Header{Typeflag: tar.TypeReg}, "12345"), "", ErrTooLarge},
		{"rejects a symlink", archive(tar.Header{Typeflag: tar.TypeSymlink, Linkname: "x"}, ""), "", ErrNotRegular},
		{"rejects a directory", archive(tar.Header{Typeflag: tar.TypeDir}, ""), "", ErrNotRegular},
		{"rejects an empty archive", strings.NewReader(""), "", io.EOF},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := readFile(tc.archive, 4)

			if string(got) != tc.want || !errors.Is(err, tc.wantErr) {
				t.Errorf("readFile() = %q, %v, want %q, %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
