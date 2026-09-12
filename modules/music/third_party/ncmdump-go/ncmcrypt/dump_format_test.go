package ncmcrypt

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDumpPreservesCodecAndAudioBytes(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		header    []byte
		extension string
	}{
		{"mp3 with id3", []byte{'I', 'D', '3', 4}, ".mp3"},
		{"mp3 without id3", []byte{0xff, 0xfb, 0x90, 0x64}, ".mp3"},
		{"flac", []byte{'f', 'L', 'a', 'C'}, ".flac"},
		{"m4a aac container", []byte{0, 0, 0, 28, 'f', 't', 'y', 'p', 'M', '4', 'A', ' ', 0, 0, 2, 0, 'M', '4', 'A', ' ', 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}, ".m4a"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			source := filepath.Join(root, "sample.ncm")
			payload := append(append([]byte{}, test.header...), make([]byte, 64)...)
			if err := os.WriteFile(source, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			stream, err := os.Open(source)
			if err != nil {
				t.Fatal(err)
			}
			defer stream.Close()
			// A zero key box leaves synthetic bytes unchanged, isolating the
			// format-selection stage without real music or encryption fixtures.
			file := &NeteaseCloudMusic{mFilePath: source, mFileStream: stream}
			outputDir := t.TempDir()
			ok, err := file.Dump(outputDir)
			if err != nil || !ok {
				t.Fatalf("Dump() = %v, %v", ok, err)
			}
			output := file.GetDumpFilePath()
			if got := filepath.Ext(output); got != test.extension {
				t.Fatalf("output extension = %q, want %q", got, test.extension)
			}
			got, err := os.ReadFile(output)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, payload) {
				t.Fatal("Dump modified the decoded audio bytes")
			}
			original, err := os.ReadFile(source)
			if err != nil || !bytes.Equal(original, payload) {
				t.Fatal("Dump modified the source")
			}
		})
	}
}
