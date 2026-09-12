package ncmcrypt

import "testing"

func TestDetectDecodedFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		header []byte
		want   NcmFormat
	}{
		{name: "mp3 with id3", header: []byte{'I', 'D', '3', 4}, want: Mp3},
		{name: "mp3 without id3", header: []byte{0xff, 0xfb, 0x90, 0x64}, want: Mp3},
		{name: "flac", header: []byte{'f', 'L', 'a', 'C'}, want: Flac},
		{name: "m4a aac container", header: []byte{0, 0, 0, 28, 'f', 't', 'y', 'p', 'M', '4', 'A', ' ', 0, 0, 2, 0, 'M', '4', 'A', ' ', 'i', 's', 'o', 'm', 'i', 's', 'o', '2'}, want: "m4a"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			got, err := detectDecodedFormat(test.header)
			if err != nil {
				t.Fatalf("detectDecodedFormat() error = %v", err)
			}
			if got != test.want {
				t.Fatalf("detectDecodedFormat() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestDetectDecodedFormatRejectsUnknownOrTruncatedData(t *testing.T) {
	t.Parallel()
	for _, header := range [][]byte{
		nil, {'f', 'L'}, {'O', 'g', 'g', 'S'}, {0xff, 0xe8, 0, 0},
		{0, 0, 0, 28, 'f', 't', 'y', 'p', 'M', '4', 'A'},
		{0, 0, 0, 8, 'f', 't', 'y', 'p', 'M', '4', 'A', ' ', 0, 0, 0, 0},
		{0, 0, 0, 28, 'f', 't', 'y', 'p', 'm', 'p', '4', '2', 0, 0, 0, 0},
	} {
		if format, err := detectDecodedFormat(header); err == nil {
			t.Fatalf("detectDecodedFormat(% x) = %q, want error", header, format)
		}
	}
}
