package ncmcrypt

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"git.taurusxin.com/taurusxin/ncmdump-go/utils"
	"github.com/bogem/id3v2/v2"
	"github.com/go-flac/flacpicture"
	"github.com/go-flac/flacvorbis"
	"github.com/go-flac/go-flac"
)

type NcmFormat = string

const (
	Mp3 NcmFormat = "mp3"
)
const (
	Flac NcmFormat = "flac"
)
const (
	M4a NcmFormat = "m4a"
)

// detectDecodedFormat identifies the codec from the decrypted audio bytes.
// Some valid MP3 files start directly with an MPEG audio frame rather than an
// ID3 tag, so treating every non-ID3 stream as FLAC corrupts format selection.
func detectDecodedFormat(header []byte) (NcmFormat, error) {
	if len(header) >= 4 && bytes.Equal(header[:4], []byte("fLaC")) {
		return Flac, nil
	}
	if len(header) >= 3 && bytes.Equal(header[:3], []byte("ID3")) {
		return Mp3, nil
	}
	if isMPEGAudioFrameHeader(header) {
		return Mp3, nil
	}
	// Only accept the verified audio-container brand, not arbitrary MP4/video
	// containers. Preserve the AAC stream and its channel layout without encoding.
	if len(header) >= 16 && binary.BigEndian.Uint32(header[:4]) >= 16 &&
		bytes.Equal(header[4:8], []byte("ftyp")) && bytes.Equal(header[8:12], []byte("M4A ")) {
		return M4a, nil
	}
	return "", fmt.Errorf("unsupported decrypted audio header: % x", headerPrefix(header, 4))
}

func isMPEGAudioFrameHeader(header []byte) bool {
	if len(header) < 4 || header[0] != 0xff || header[1]&0xe0 != 0xe0 {
		return false
	}
	version := (header[1] >> 3) & 0x03
	layer := (header[1] >> 1) & 0x03
	bitrateIndex := header[2] >> 4
	sampleRateIndex := (header[2] >> 2) & 0x03
	return version != 0x01 && layer != 0 && bitrateIndex != 0x0f && sampleRateIndex != 0x03
}

func headerPrefix(header []byte, limit int) []byte {
	if len(header) < limit {
		return header
	}
	return header[:limit]
}

type NeteaseCloudMusic struct {
	sCoreKey   [17]byte
	sModifyKey [17]byte
	mPng       [8]byte

	mFilePath string

	mDumpFilePath string
	mFormat       NcmFormat
	mImageData    []byte
	mFileStream   *os.File
	mKeyBox       [256]byte
	mMetadata     *NeteaseClousMusicMetadata
	mAlbumPicUrl  string
}

func (ncm *NeteaseCloudMusic) read(buffer *[]byte, size int) int {
	if len(*buffer) < size {
		*buffer = make([]byte, size)
	}
	res, err := ncm.mFileStream.Read(*buffer)
	if err != nil {
		return 0
	}
	return res
}

func (ncm *NeteaseCloudMusic) openFile() bool {
	file, err := os.Open(ncm.mFilePath)
	if err != nil {
		return false
	}
	ncm.mFileStream = file
	return true
}

func (ncm *NeteaseCloudMusic) isNcmFile() bool {
	header := make([]byte, 4)

	// check magic header 4E455443 4D414446
	if ncm.read(&header, 4) != 4 {
		return false
	}
	if int(binary.LittleEndian.Uint32(header)) != 0x4E455443 {
		return false
	}
	if ncm.read(&header, 4) != 4 {
		return false
	}

	if int(binary.LittleEndian.Uint32(header)) != 0x4D414446 {
		return false
	}

	return true
}

func (ncm *NeteaseCloudMusic) buildKeyBox(key []byte, keyLen int) {
	for i := 0; i < 256; i++ {
		ncm.mKeyBox[i] = byte(i)
	}

	var swap uint8 = 0
	var c uint8 = 0
	var lastByte uint8 = 0
	var keyOffset uint8 = 0

	for i := 0; i < 256; i++ {
		swap = ncm.mKeyBox[i]
		c = (swap + lastByte + key[keyOffset]) & 0xff
		keyOffset++
		if int(keyOffset) >= keyLen {
			keyOffset = 0
		}
		ncm.mKeyBox[i] = ncm.mKeyBox[c]
		ncm.mKeyBox[c] = swap
		lastByte = c
	}
}

func (ncm *NeteaseCloudMusic) mimeType() string {
	if bytes.HasPrefix(ncm.mImageData, ncm.mPng[:]) {
		return "image/png"
	}
	return "image/jpeg"
}

// Dump encrypted ncm file to normal music file. If `targetDir` is "", the converted file will be saved to the original directory.
func (ncm *NeteaseCloudMusic) Dump(targetDir string) (bool, error) {
	ncm.mDumpFilePath = ncm.mFilePath
	var outputStream *os.File

	buffer := make([]byte, 0x8000)
	findFormatFlag := false

	for {
		n := ncm.read(&buffer, len(buffer))

		if n == 0 {
			break
		}

		for i := 0; i < n; i++ {
			j := (i + 1) & 0xff
			buffer[i] ^= ncm.mKeyBox[(ncm.mKeyBox[j]+ncm.mKeyBox[(int(ncm.mKeyBox[j])+j)&0xff])&0xff]
		}

		if !findFormatFlag {
			format, err := detectDecodedFormat(buffer[:n])
			if err != nil {
				return false, err
			}
			ncm.mFormat = format
			ncm.mDumpFilePath = utils.ReplaceExtension(ncm.mDumpFilePath, "."+format)
			if targetDir != "" { // change save dir
				ncm.mDumpFilePath = filepath.Join(targetDir, filepath.Base(ncm.mDumpFilePath))
			}
			findFormatFlag = true

			output, err := os.Create(ncm.mDumpFilePath)
			if err != nil {
				return false, fmt.Errorf("create output file failed")
			}
			outputStream = output
		}

		outputStream.Write(buffer[:n])
	}

	outputStream.Close()
	return true, nil
}

// FixMetadata will fix the missing metadata for target music file, the source of the metadata comes from origin ncm file.
// Since NeteaseCloudMusic version 3.0, the album cover image is no longer embedded in the ncm file. If the parameter is true, it means downloading the image from the NetEase server and embedding it into the target music file (network connection required)
func (ncm *NeteaseCloudMusic) FixMetadata(fetchAlbumImageFromRemote bool) (bool, error) {
	// only fetch album image from remote when it's not embedded in the ncm file
	if len(ncm.mImageData) <= 0 && fetchAlbumImageFromRemote {
		// get the album pic from url
		resp, err := http.Get(ncm.mAlbumPicUrl)
		if err != nil {
			return false, err
		}
		if resp != nil {
			if resp.StatusCode == http.StatusOK {
				bodyBytes, err := io.ReadAll(resp.Body)
				if err != nil {
					return false, err
				}
				ncm.mImageData = bodyBytes
			}
		}
	}
	switch ncm.mFormat {
	case Mp3:
		audioFile, err := id3v2.Open(ncm.mDumpFilePath, id3v2.Options{Parse: true})
		if err != nil {
			return false, err
		}
		defer audioFile.Close()
		audioFile.SetDefaultEncoding(id3v2.EncodingUTF8)
		audioFile.SetTitle(ncm.mMetadata.mName)
		audioFile.SetArtist(ncm.mMetadata.mArtist)
		audioFile.SetAlbum(ncm.mMetadata.mAlbum)

		if len(ncm.mImageData) > 0 {
			pic := id3v2.PictureFrame{
				Encoding:    id3v2.EncodingUTF8,
				MimeType:    ncm.mimeType(),
				PictureType: id3v2.PTFrontCover,
				Description: "",
				Picture:     ncm.mImageData,
			}
			audioFile.AddAttachedPicture(pic)
		}

		err = audioFile.Save()
		if err != nil {
			return false, err
		}
	case Flac:
		audioFile, err := flac.ParseFile(ncm.mDumpFilePath)
		if err != nil {
			return false, err
		}
		if len(ncm.mImageData) > 0 {
			pic, err := flacpicture.NewFromImageData(flacpicture.PictureTypeFrontCover, "",
				ncm.mImageData, ncm.mimeType())
			if err != nil {
				return false, err
			}
			pictureMeta := pic.Marshal()
			audioFile.Meta = append(audioFile.Meta, &pictureMeta)
		}

		var cmts *flacvorbis.MetaDataBlockVorbisComment
		var cmtIdx int
		for idx, meta := range audioFile.Meta {
			if meta.Type == flac.VorbisComment {
				cmts, err = flacvorbis.ParseFromMetaDataBlock(*meta)
				cmtIdx = idx
				if err != nil {
					return false, err
				}
			}
		}
		if cmts == nil && cmtIdx > 0 {
			cmts = flacvorbis.New()
		}

		// flac 可能自带元数据 当且仅当没有该项时才向目标添加元数据
		if res, _ := cmts.Get(flacvorbis.FIELD_TITLE); len(res) == 0 {
			_ = cmts.Add(flacvorbis.FIELD_TITLE, ncm.mMetadata.mName)
		}
		if res, _ := cmts.Get(flacvorbis.FIELD_ARTIST); len(res) == 0 {
			_ = cmts.Add(flacvorbis.FIELD_ARTIST, ncm.mMetadata.mArtist)
		}
		if res, _ := cmts.Get(flacvorbis.FIELD_ALBUM); len(res) == 0 {
			_ = cmts.Add(flacvorbis.FIELD_ALBUM, ncm.mMetadata.mAlbum)
		}

		cmtsmeta := cmts.Marshal()

		if cmtIdx > 0 {
			audioFile.Meta[cmtIdx] = &cmtsmeta
		} else {
			audioFile.Meta = append(audioFile.Meta, &cmtsmeta)
		}

		err = audioFile.Save(ncm.mDumpFilePath)

		if err != nil {
			return false, err
		}
	}
	return true, nil
}

// GetDumpFilePath returns the absolute path of dumped music file
func (ncm *NeteaseCloudMusic) GetDumpFilePath() string {
	path, err := filepath.Abs(ncm.mDumpFilePath)
	if err != nil {
		return ncm.mDumpFilePath
	}
	return path
}

// NewNeteaseCloudMusic returns a new NeteaseCloudMusic instance, if the format of the file is incorrect, the error will be returned.
func NewNeteaseCloudMusic(filePath string) (*NeteaseCloudMusic, error) {
	ncm := &NeteaseCloudMusic{
		sCoreKey:   [17]byte{0x68, 0x7A, 0x48, 0x52, 0x41, 0x6D, 0x73, 0x6F, 0x35, 0x6B, 0x49, 0x6E, 0x62, 0x61, 0x78, 0x57, 0},
		sModifyKey: [17]byte{0x23, 0x31, 0x34, 0x6C, 0x6A, 0x6B, 0x5F, 0x21, 0x5C, 0x5D, 0x26, 0x30, 0x55, 0x3C, 0x27, 0x28, 0},
		mPng:       [8]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A},

		mFilePath: filePath,
	}

	if !ncm.openFile() {
		return nil, fmt.Errorf("open file failed")
	}

	if !ncm.isNcmFile() {
		return nil, fmt.Errorf("not a ncm file")
	}

	// actually this 2 bytes is the version, now we just skip it
	if _, err := ncm.mFileStream.Seek(2, 1); err != nil {
		return nil, fmt.Errorf("seek version failed")
	}

	// the length of the RC4 key, encrypted by AES128
	n := make([]byte, 4)

	if ncm.read(&n, len(n)) != 4 {
		return nil, fmt.Errorf("read key len failed")
	}

	keyLen := int(binary.LittleEndian.Uint32(n))

	keydata := make([]byte, keyLen)
	ncm.read(&keydata, keyLen)

	for i := range keydata {
		keydata[i] ^= 0x64
	}

	mKeyData, err := utils.AesEcbDecrypt(ncm.sCoreKey[:16], keydata)

	if err != nil {
		return nil, fmt.Errorf("decrypt key failed")
	}

	// build the key box
	ncm.buildKeyBox(mKeyData[17:], len(mKeyData)-17)

	if ncm.read(&n, len(n)) != 4 {
		return nil, fmt.Errorf("read metadata len failed")
	}

	metadataLen := int(binary.LittleEndian.Uint32(n))

	if metadataLen <= 0 {
		// process meta here
		ncm.mMetadata = nil
	} else {
		// read metadata
		modifyData := make([]byte, metadataLen)
		ncm.read(&modifyData, metadataLen)

		for i := range modifyData {
			modifyData[i] ^= 0x63
		}

		// escape `163 key(Don't modify):`
		swapModifyData := string(modifyData[22:])

		modifyOutData, err := base64.StdEncoding.DecodeString(swapModifyData)
		if err != nil {
			panic("base64 decode modify data failed")
		}

		modifyDecryptData, err := utils.AesEcbDecrypt(ncm.sModifyKey[:16], modifyOutData)

		if err != nil {
			panic("decrypt modify data failed")
		}

		// escape `music:`
		mMetadataString := string(modifyDecryptData[6:])

		// extract the album pic url
		ncm.mAlbumPicUrl = GetAlbumPicUrl(mMetadataString)

		ncm.mMetadata = NewNeteaseCloudMusicMetadata(mMetadataString)
	}

	// skip the 5 bytes gap
	if _, err := ncm.mFileStream.Seek(5, 1); err != nil {
		return nil, fmt.Errorf("seek gap failed")
	}

	// read the cover frame
	coverFrameLen := make([]byte, 4)

	if ncm.read(&coverFrameLen, len(coverFrameLen)) != 4 {
		return nil, fmt.Errorf("read cover frame len failed")
	}

	if ncm.read(&n, len(n)) != 4 {
		return nil, fmt.Errorf("read cover frame data len failed")
	}

	coverFrameLenInt := int(binary.LittleEndian.Uint32(coverFrameLen))
	coverFrameDataLen := int(binary.LittleEndian.Uint32(n))

	if coverFrameDataLen > 0 {
		ncm.read(&ncm.mImageData, coverFrameDataLen)
	}

	ncm.mFileStream.Seek(int64(coverFrameLenInt)-int64(coverFrameDataLen), 1)

	return ncm, nil
}
