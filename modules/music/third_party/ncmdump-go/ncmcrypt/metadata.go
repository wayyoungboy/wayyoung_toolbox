package ncmcrypt

import (
	"github.com/tidwall/gjson"
)

type NeteaseClousMusicMetadata struct {
	mAlbum    string
	mArtist   string
	mFormat   string
	mName     string
	mDuration int64
	mBitrate  int64
}

func NewNeteaseCloudMusicMetadata(meta string) *NeteaseClousMusicMetadata {
	if meta == "" {
		return nil
	}

	metaData := &NeteaseClousMusicMetadata{
		mAlbum:    "",
		mArtist:   "",
		mFormat:   "",
		mName:     "",
		mDuration: 0,
		mBitrate:  0,
	}

	metaData.mName = gjson.Get(meta, "musicName").String()
	metaData.mAlbum = gjson.Get(meta, "album").String()

	artists := gjson.Get(meta, "artist").Array()
	if len(artists) > 0 {
		for i, artist := range artists {
			if i > 0 {
				metaData.mArtist += " / "
			}
			metaData.mArtist += artist.Array()[0].String()
		}
	}

	metaData.mBitrate = gjson.Get(meta, "bitrate").Int()
	metaData.mDuration = gjson.Get(meta, "duration").Int()
	metaData.mFormat = gjson.Get(meta, "format").String()

	return metaData
}

func GetAlbumPicUrl(meta string) string {
	return gjson.Get(meta, "albumPic").String()
}
