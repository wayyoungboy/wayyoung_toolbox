package sites

import (
	"github.com/yuukiy/javsp-go/internal/crawler"
	"github.com/yuukiy/javsp-go/internal/httpclient"
)

type RegistryOptions struct {
	BaseURLs            map[string]string
	Hardworking         bool
	FC2FanLocalPath     string
	JavDBCookieProvider JavDBCookieProvider
}

func RegisterAll(registry *crawler.Registry, client *httpclient.Client, opts RegistryOptions) error {
	crawlers := []crawler.Crawler{
		NewAirAV(client, registryBaseURL(opts, "airav", "https://www.airav.wiki"), opts.Hardworking),
		NewAVSox(client, registryBaseURL(opts, "avsox", "https://avsox.click")),
		NewAVWiki(client, registryBaseURL(opts, "avwiki", "https://av-wiki.net")),
		NewDLGetchu(client, registryBaseURL(opts, "dl_getchu", "https://dl.getchu.com")),
		NewFanza(client, registryBaseURL(opts, "fanza", "https://www.dmm.co.jp"), opts.Hardworking),
		NewFaleno(client, registryBaseURL(opts, "faleno", "https://faleno.jp/top")),
		NewFC2(client, registryBaseURL(opts, "fc2", "https://adult.contents.fc2.com"), opts.Hardworking),
		NewFC2DB(client, registryBaseURL(opts, "fc2db", "https://fc2db.net")),
		NewFC2Fan(client, registryBaseURL(opts, "fc2fan", "https://fc2club.top"), opts.FC2FanLocalPath),
		NewFC2PPVDB(client, registryBaseURL(opts, "fc2ppvdb", "https://fc2ppvdb.com")),
		NewGyutto(client, registryBaseURL(opts, "gyutto", "http://gyutto.com")),
		NewJav321(client, registryBaseURL(opts, "jav321", "https://www.jav321.com")),
		NewJavBus(client, registryBaseURL(opts, "javbus", "https://www.javbus.com")),
		NewJavDBWithCookieProvider(client, registryBaseURL(opts, "javdb", "https://javdb.com"), opts.JavDBCookieProvider),
		NewJavLib(client, registryBaseURL(opts, "javlib", "https://www.javlibrary.com")),
		NewJavMenu(client, registryBaseURL(opts, "javmenu", "https://mrzyx.xyz")),
		NewMGStage(client, registryBaseURL(opts, "mgstage", "https://www.mgstage.com"), opts.Hardworking),
		NewNJAV(client, registryBaseURL(opts, "njav", "https://njav.tv/ja")),
		NewPrestige(client, registryBaseURL(opts, "prestige", "https://www.prestige-av.com")),
		NewArzon(client, registryBaseURL(opts, "arzon", "https://www.arzon.jp"), false),
		NewArzonIV(client, registryBaseURL(opts, "arzon_iv", "https://www.arzon.jp")),
	}
	for _, item := range crawlers {
		if err := registry.Register(item); err != nil {
			return err
		}
	}
	return nil
}

func registryBaseURL(opts RegistryOptions, id string, fallback string) string {
	if opts.BaseURLs != nil {
		if value := opts.BaseURLs[id]; value != "" {
			return value
		}
	}
	return fallback
}
