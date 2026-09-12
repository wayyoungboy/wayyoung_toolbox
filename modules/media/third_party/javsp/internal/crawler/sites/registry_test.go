package sites

import (
	"context"
	"net/http"
	"testing"

	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
	"github.com/yuukiy/javsp-go/internal/model"
)

func TestRegisterAllRegistersSupportedCrawlers(t *testing.T) {
	client, err := httpclient.New(httpclient.Options{})
	if err != nil {
		t.Fatal(err)
	}
	registry := crawler.NewRegistry()
	err = RegisterAll(registry, client, RegistryOptions{
		BaseURLs: map[string]string{
			"javbus": "https://mirror.example",
			"javdb":  "https://db.example",
		},
		Hardworking:         true,
		FC2FanLocalPath:     "/tmp/fc2fan",
		JavDBCookieProvider: fakeRegistryCookieProvider{},
	})
	if err != nil {
		t.Fatalf("RegisterAll returned error: %v", err)
	}

	ids := []model.CrawlerID{
		"airav",
		"avsox",
		"avwiki",
		"dl_getchu",
		"fanza",
		"faleno",
		"fc2",
		"fc2fan",
		"fc2ppvdb",
		"gyutto",
		"jav321",
		"javbus",
		"javdb",
		"javlib",
		"javmenu",
		"mgstage",
		"njav",
		"prestige",
		"arzon",
		"arzon_iv",
	}
	for _, id := range ids {
		item, ok := registry.Get(id)
		if !ok {
			t.Fatalf("crawler %s was not registered", id)
		}
		if item.ID() != id {
			t.Fatalf("crawler %s has ID %s", id, item.ID())
		}
	}

	javbus, _ := registry.Get("javbus")
	if got := javbus.(*JavBus).baseURL; got != "https://mirror.example" {
		t.Fatalf("javbus baseURL = %q", got)
	}
	javdb, _ := registry.Get("javdb")
	if got := javdb.(*JavDB).baseURL; got != "https://db.example" {
		t.Fatalf("javdb baseURL = %q", got)
	}
	if javdb.(*JavDB).cookieProvider == nil {
		t.Fatalf("javdb cookie provider was not propagated")
	}
	fanza, _ := registry.Get("fanza")
	if !fanza.(*Fanza).hardworking {
		t.Fatalf("fanza hardworking was not propagated")
	}
	fc2fan, _ := registry.Get("fc2fan")
	if got := fc2fan.(*FC2Fan).localPath; got != "/tmp/fc2fan" {
		t.Fatalf("fc2fan localPath = %q", got)
	}
}

type fakeRegistryCookieProvider struct{}

func (fakeRegistryCookieProvider) NextJavDBCookies(context.Context) ([]*http.Cookie, bool) {
	return nil, false
}
