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
	// 获取配置地址
	addr := config.Cfg.Emby.Addr
	
	// 补全 http 前缀 (使用 strings 包，解决编译报错)
	if !strings.HasPrefix(addr, "http") {
		addr = "http://" + addr
	}
	// 去除末尾斜杠
	addr = strings.TrimSuffix(addr, "/")

	target, _ := url.Parse(addr)
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
	params.Add("Limit", strconv.Itoa(1))
	params.Add("Fields", "Path,MediaSources")
	params.Add("Recursive", "true")
	params.Add("api_key", config.Cfg.Emby.ApiKey)

	// 使用处理过的 Target 地址，而不是直接用 config 中的原始地址
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

// 辅助函数：创建 Bool 指针 (供 proxy 包调用)
func BoolPtr(b bool) *bool {
	return &b
}

// 辅助函数：创建 String 指针 (供 proxy 包调用)
func StrPtr(s string) *string {
	return &s
}