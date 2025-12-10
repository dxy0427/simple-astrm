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
	// 使用 strconv 解决 "imported and not used" 错误，同时限制查询数量为 1
	params.Add("Limit", strconv.Itoa(1)) 
	params.Add("Fields", "Path,MediaSources")
	params.Add("Recursive", "true")
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

// ================= 结构体定义 (参考原项目使用指针) =================

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
	ID                     *string `json:"Id"`
	Path                   *string `json:"Path"`
	Protocol               *string `json:"Protocol"`
	ItemID                 *string `json:"ItemId"`
	Name                   *string `json:"Name"`
	Container              *string `json:"Container"`
	SupportsDirectPlay     *bool   `json:"SupportsDirectPlay"`
	SupportsDirectStream   *bool   `json:"SupportsDirectStream"`
	SupportsTranscoding    *bool   `json:"SupportsTranscoding"`
	DirectStreamUrl        *string `json:"DirectStreamUrl,omitempty"`
	TranscodingUrl         *string `json:"TranscodingUrl,omitempty"`
	TranscodingContainer   *string `json:"TranscodingContainer,omitempty"`
	TranscodingSubProtocol *string `json:"TranscodingSubProtocol,omitempty"`
}

// 辅助函数：创建 Bool 指针
func BoolPtr(b bool) *bool {
	return &b
}

// 辅助函数：创建 String 指针
func StrPtr(s string) *string {
	return &s
}