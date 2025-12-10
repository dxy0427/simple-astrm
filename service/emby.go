package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"simple-astrm/config"
	"strconv"
	"strings"
)

type EmbyServer struct {
	Target *url.URL
	Proxy  *httputil.ReverseProxy
}

func NewEmbyServer() *EmbyServer {
	addr := config.Cfg.Emby.Addr
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	addr = strings.TrimSuffix(addr, "/")

	target, _ := url.Parse(addr)
	proxy := httputil.NewSingleHostReverseProxy(target)

	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	return &EmbyServer{Target: target, Proxy: proxy}
}

func (s *EmbyServer) QueryItem(ids string) (*EmbyItemsResponse, error) {
	params := url.Values{}
	params.Add("Ids", ids)
	params.Add("Limit", strconv.Itoa(1))
	params.Add("Fields", "Path,MediaSources")
	params.Add("Recursive", "true")
	params.Add("api_key", config.Cfg.Emby.ApiKey)

	api := s.Target.String() + "/Items?" + params.Encode()
	resp, err := http.Get(api)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var res EmbyItemsResponse
	json.Unmarshal(body, &res)
	return &res, nil
}

// ================= 完整结构体定义 =================

type EmbyItemsResponse struct {
	Items []BaseItemDto `json:"Items"`
}

type PlaybackInfoResponse struct {
	MediaSources []MediaSourceInfo `json:"MediaSources"`
}

type BaseItemDto struct {
	Path         *string           `json:"Path"`
	MediaSources []MediaSourceInfo `json:"MediaSources"`
}

type MediaSourceInfo struct {
	ID                     *string           `json:"Id"`
	Path                   *string           `json:"Path"`
	Protocol               *string           `json:"Protocol"`
	ItemID                 *string           `json:"ItemId"`
	Name                   *string           `json:"Name"`
	Container              *string           `json:"Container"`
	SupportsDirectPlay     *bool             `json:"SupportsDirectPlay"`
	SupportsDirectStream   *bool             `json:"SupportsDirectStream"`
	SupportsTranscoding    *bool             `json:"SupportsTranscoding"`
	DirectStreamUrl        *string           `json:"DirectStreamUrl,omitempty"`
	TranscodingUrl         *string           `json:"TranscodingUrl,omitempty"`
	TranscodingContainer   *string           `json:"TranscodingContainer,omitempty"`
	TranscodingSubProtocol *string           `json:"TranscodingSubProtocol,omitempty"`
	MediaStreams           []MediaStreamInfo `json:"MediaStreams"`
	Bitrate                *int64            `json:"Bitrate,omitempty"`
	RunTimeTicks           *int64            `json:"RunTimeTicks,omitempty"`
	Size                   *int64            `json:"Size,omitempty"`
}

type MediaStreamInfo struct {
	Codec                  *string  `json:"Codec,omitempty"`
	CodecTag               *string  `json:"CodecTag,omitempty"`
	Language               *string  `json:"Language,omitempty"`
	ColorRange             *string  `json:"ColorRange,omitempty"`
	ColorSpace             *string  `json:"ColorSpace,omitempty"`
	ColorTransfer          *string  `json:"ColorTransfer,omitempty"`
	ColorPrimaries         *string  `json:"ColorPrimaries,omitempty"`
	Comment                *string  `json:"Comment,omitempty"`
	TimeBase               *string  `json:"TimeBase,omitempty"`
	CodecTimeBase          *string  `json:"CodecTimeBase,omitempty"`
	Title                  *string  `json:"Title,omitempty"`
	VideoRange             *string  `json:"VideoRange,omitempty"`
	DisplayTitle           *string  `json:"DisplayTitle,omitempty"`
	NalLengthSize          *string  `json:"NalLengthSize,omitempty"`
	IsInterlaced           *bool    `json:"IsInterlaced,omitempty"`
	IsAVC                  *bool    `json:"IsAVC,omitempty"`
	ChannelLayout          *string  `json:"ChannelLayout,omitempty"`
	BitRate                *int64   `json:"BitRate,omitempty"`
	BitDepth               *int64   `json:"BitDepth,omitempty"`
	RefFrames              *int64   `json:"RefFrames,omitempty"`
	Rotation               *int64   `json:"Rotation,omitempty"`
	Channels               *int64   `json:"Channels,omitempty"`
	SampleRate             *int64   `json:"SampleRate,omitempty"`
	Width                  *int64   `json:"Width,omitempty"`
	Height                 *int64   `json:"Height,omitempty"`
	AverageFrameRate       *float64 `json:"AverageFrameRate,omitempty"`
	RealFrameRate          *float64 `json:"RealFrameRate,omitempty"`
	Profile                *string  `json:"Profile,omitempty"`
	Type                   *string  `json:"Type,omitempty"`
	AspectRatio            *string  `json:"AspectRatio,omitempty"`
	Index                  *int64   `json:"Index,omitempty"`
	IsExternal             *bool    `json:"IsExternal,omitempty"`
	IsTextSubtitleStream   *bool    `json:"IsTextSubtitleStream,omitempty"`
	SupportsExternalStream *bool    `json:"SupportsExternalStream,omitempty"`
	PixelFormat            *string  `json:"PixelFormat,omitempty"`
	Level                  *float64 `json:"Level,omitempty"`
}

func BoolPtr(b bool) *bool { return &b }
func StrPtr(s string) *string  { return &s }