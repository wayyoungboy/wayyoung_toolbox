package service

import (
 "context"
 "errors"
 "net"
 "net/http"
 "path/filepath"
 "testing"
 "time"
 "github.com/wayyoungboy/fnos-media-acquisition-center/internal/playback"
)
func TestPlaybackShutdownCanFinishAfterCallerDeadline(t *testing.T){
 s:=testService(t)
 entered,release:=make(chan struct{}),make(chan struct{})
 manager,err:=playback.New(playback.Options{Dir:filepath.Join(t.TempDir(),"cache"),FFprobe:"not-used",Open:func(context.Context,string)(playback.Source,error){close(entered);<-release;return playback.Source{},playback.ErrUnavailable}})
 if err!=nil{t.Fatal(err)};s.playback=manager
 go manager.Probe(context.Background(),"asset");<-entered
 listener,err:=net.Listen("tcp","127.0.0.1:0");if err!=nil{t.Fatal(err)}
 s.server=&http.Server{Handler:s.Handler()};defer s.server.Close();go s.server.Serve(listener)
 ctx,cancel:=context.WithTimeout(context.Background(),time.Millisecond);defer cancel()
 err=s.Shutdown(ctx)
 close(release)
 if !errors.Is(err,context.DeadlineExceeded){t.Fatalf("caller deadline=%v",err)}
 completed,cancelCompleted:=context.WithTimeout(context.Background(),2*time.Second);defer cancelCompleted()
 if err:=s.Shutdown(completed);err!=nil{t.Fatalf("shutdown could not finish after deadline: %v",err)}
 if err:=s.db.db.Ping();err==nil{t.Fatal("database remained open")}
 connection,err:=net.DialTimeout("tcp",listener.Addr().String(),time.Second);if err==nil{connection.Close();t.Fatal("HTTP listener remained open")}
}
