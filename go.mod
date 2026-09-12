module github.com/wayyoungboy/wayyoung_toolbox

go 1.25.13

require (
	fnos-backup-center v0.0.0
	github.com/wayyoungboy/fnos-media-acquisition-center v0.0.0
	github.com/wayyoungboy/fnos-music-converter v0.0.0
	github.com/wayyoungboy/ha500-fnos-toolbox v0.0.0
)

replace fnos-backup-center => ./modules/backup

replace github.com/wayyoungboy/fnos-media-acquisition-center => ./modules/media

replace github.com/wayyoungboy/fnos-music-converter => ./modules/music

replace github.com/wayyoungboy/ha500-fnos-toolbox => ./modules/ha500

replace github.com/yuukiy/javsp-go => ./modules/media/third_party/javsp

replace git.taurusxin.com/taurusxin/ncmdump-go => ./modules/music/third_party/ncmdump-go

require (
	git.taurusxin.com/taurusxin/ncmdump-go v1.7.5 // indirect
	github.com/TwiN/go-color v1.4.1 // indirect
	github.com/antchfx/htmlquery v1.3.6 // indirect
	github.com/antchfx/xpath v1.3.6 // indirect
	github.com/bogem/id3v2/v2 v2.1.4 // indirect
	github.com/disintegration/imaging v1.6.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/esimov/pigo v1.4.6 // indirect
	github.com/go-flac/flacpicture v0.3.0 // indirect
	github.com/go-flac/flacvorbis v0.2.0 // indirect
	github.com/go-flac/go-flac v1.0.0 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mmcdole/gofeed v1.4.0 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/tidwall/gjson v1.17.3 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.1 // indirect
	github.com/yuukiy/javsp-go v0.0.0 // indirect
	golang.org/x/image v0.45.0 // indirect
	golang.org/x/net v0.57.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
	modernc.org/sqlite v1.56.0 // indirect
)
