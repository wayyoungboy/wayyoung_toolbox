module github.com/wayyoungboy/fnos-media-acquisition-center

go 1.25.13

require (
	github.com/klauspost/compress v1.20.3
	github.com/mmcdole/gofeed v1.4.0
	github.com/pierrec/lz4/v4 v4.1.33
	github.com/ulikunitz/xz v0.5.17
	github.com/yuukiy/javsp-go v0.0.0
	golang.org/x/image v0.45.0
	golang.org/x/net v0.57.0
	golang.org/x/sys v0.47.0
	golang.org/x/text v0.41.0
	gopkg.in/yaml.v3 v3.0.1
	modernc.org/sqlite v1.56.0
)

replace github.com/yuukiy/javsp-go => ./third_party/javsp

require (
	github.com/antchfx/htmlquery v1.3.6 // indirect
	github.com/antchfx/xpath v1.3.6 // indirect
	github.com/disintegration/imaging v1.6.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/esimov/pigo v1.4.6 // indirect
	github.com/golang/groupcache v0.0.0-20210331224755-41bb18bfe9da // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/mmcdole/goxpp/v2 v2.0.0 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	modernc.org/libc v1.74.4 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
