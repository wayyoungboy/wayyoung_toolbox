package httpclient

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
)

type Info struct {
	Total   int64
	Elapsed time.Duration
	Rate    float64
}

func Download(ctx context.Context, client *Client, source string, destination string) (Info, error) {
	start := time.Now()
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	if !strings.HasPrefix(source, "http://") && !strings.HasPrefix(source, "https://") {
		return copyLocal(ctx, start, source, destination)
	}
	if client == nil {
		var err error
		client, err = New(Options{})
		if err != nil {
			return Info{}, err
		}
	}
	resp, err := client.Get(ctx, source)
	if err != nil {
		return Info{}, err
	}
	defer resp.Body.Close()

	out, err := os.Create(destination)
	if err != nil {
		return Info{}, err
	}
	defer out.Close()
	total, err := io.Copy(out, &contextReader{ctx: ctx, reader: resp.Body})
	if err != nil {
		return Info{}, err
	}
	return infoFrom(start, total), nil
}

func copyLocal(ctx context.Context, start time.Time, source string, destination string) (Info, error) {
	if err := ctx.Err(); err != nil {
		return Info{}, err
	}
	in, err := os.Open(source)
	if err != nil {
		return Info{}, err
	}
	defer in.Close()
	out, err := os.Create(destination)
	if err != nil {
		return Info{}, err
	}
	defer out.Close()
	total, err := io.Copy(out, &contextReader{ctx: ctx, reader: in})
	if err != nil {
		return Info{}, err
	}
	return infoFrom(start, total), nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

func infoFrom(start time.Time, total int64) Info {
	elapsed := time.Since(start)
	rate := 0.0
	if elapsed > 0 {
		rate = float64(total) / elapsed.Seconds()
	}
	return Info{Total: total, Elapsed: elapsed, Rate: rate}
}
