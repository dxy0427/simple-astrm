package service

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"simple-astrm/config"
)

type EmbyServer struct {
	Target *url.URL
	Proxy  *httputil.ReverseProxy
}

func NewEmbyServer() *EmbyServer {
	target, _ := url.Parse(config.Cfg.Emby.Addr)
	proxy := httputil.NewSingleHostReverseProxy(target)
	
	// 自定义 Director 以保留 Host 头
	originalDirector := proxy.Director
	proxy.Director = func(req *http.Request) {
		originalDirector(req)
		req.Host = target.Host
	}

	return &EmbyServer{Target: target, Proxy: proxy}
}

// 查询 Emby Item 信息
func (s *EmbyServer) QueryItem(ids string) (*EmbyItemsResponse, error) {
	params := url.Values{}
	params.Add("Ids", ids)
	params.Add("Fields", "Path,MediaSources")
	params.Add("api_key", config.Cfg.Emby.ApiKey)

	api := config.Cfg.Emby.Addr + "/Items?" + params.Encode()
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

// 结构体定义 (只保留关键字段)
type EmbyItemsResponse struct {
	Items []struct {
		Path         string        `json:"Path"`
		MediaSources []MediaSource `json:"MediaSources"`
	} `json:"Items"`
}

type MediaSource struct {
	ID                   string `json:"Id"`
	Path                 string `json:"Path"`
	Protocol             string `json:"Protocol"`
	SupportsDirectPlay   bool   `json:"SupportsDirectPlay"`
	SupportsDirectStream bool   `json:"SupportsDirectStream"`
	SupportsTranscoding  bool   `json:"SupportsTranscoding"` // 修复编译错误：新增此字段
	DirectStreamUrl      string `json:"DirectStreamUrl,omitempty"`
	TranscodingUrl       string `json:"TranscodingUrl,omitempty"`
	TranscodingContainer string `json:"TranscodingContainer,omitempty"`
}

type PlaybackInfoResponse struct {
	MediaSources []MediaSource `json:"MediaSources"`
}