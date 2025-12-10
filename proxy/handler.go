package proxy

import (
	"bytes"
	"encoding/json"
	"net/http"
	"regexp"
	"simple-astrm/config"
	"simple-astrm/service"
	"simple-astrm/utils"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

var (
	embyServer *service.EmbyServer
	// 正则匹配
	regexPlaybackInfo = regexp.MustCompile(`(?i)/Items/\d+/PlaybackInfo`)
	regexBaseJs       = regexp.MustCompile(`(?i)basehtmlplayer.js`)
)

func Init(r *gin.Engine) {
	embyServer = service.NewEmbyServer()
	r.NoRoute(Handler)
}

func Handler(c *gin.Context) {
	path := c.Request.URL.Path

	// 1. 修改 PlaybackInfo (直接替换直连地址)
	if regexPlaybackInfo.MatchString(path) {
		handlePlaybackInfo(c)
		return
	}

	// 2. 修改 JS (解决跨域混排)
	if regexBaseJs.MatchString(path) {
		handleBaseJs(c)
		return
	}

	// 其他请求默认代理
	embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
}

// 拦截 PlaybackInfo，直接修改 DirectStreamUrl 为最终的目标地址
func handlePlaybackInfo(c *gin.Context) {
	proxy := *embyServer.Proxy
	proxy.ModifyResponse = func(resp *http.Response) error {
		if resp.StatusCode != 200 {
			return nil
		}
		bodyBytes, err := utils.ReadBody(resp)
		if err != nil {
			return err
		}

		var info service.PlaybackInfoResponse
		if err := json.Unmarshal(bodyBytes, &info); err != nil {
			return err
		}

		cfg := config.Cfg.Emby.HttpStrm

		for i, ms := range info.MediaSources {
			// 只要 Emby 识别为 Http/Https 协议，就视为 Strm/远程流，应用替换规则
			if ms.Protocol != nil && (*ms.Protocol == "Http" || *ms.Protocol == "Https") {
				
				// 1. 强制直连
				info.MediaSources[i].SupportsDirectPlay = service.BoolPtr(true)
				info.MediaSources[i].SupportsDirectStream = service.BoolPtr(true)

				// 2. 禁用转码 (根据配置)
				if !cfg.TransCode {
					info.MediaSources[i].SupportsTranscoding = service.BoolPtr(false)
					info.MediaSources[i].TranscodingUrl = nil
					info.MediaSources[i].TranscodingContainer = nil
					info.MediaSources[i].TranscodingSubProtocol = nil
				}

				// 3. 执行 URL 替换
				if ms.Path != nil {
					finalURL := *ms.Path
					
					// 循环执行配置中的替换规则
					for _, action := range cfg.Actions {
						if action.Type == "replace" {
							parts := strings.Split(action.Args, "->")
							if len(parts) == 2 {
								oldVal := strings.TrimSpace(parts[0])
								newVal := strings.TrimSpace(parts[1])
								finalURL = strings.ReplaceAll(finalURL, oldVal, newVal)
							}
						}
					}

					// 获取最终跳转 URL (如果开启 FinalURL)
					if cfg.FinalURL {
						if u, err := utils.GetFinalURL(finalURL, c.Request.UserAgent()); err == nil {
							finalURL = u
						}
					}

					// 直接修改返回给客户端的 Path 和 DirectStreamUrl
					// 客户端收到这个后，会直接请求这个 URL，不再经过代理
					info.MediaSources[i].Path = service.StrPtr(finalURL)
					info.MediaSources[i].DirectStreamUrl = service.StrPtr(finalURL)

					logrus.Infof("规则匹配 | 原始: %s | 替换后: %s", *ms.Path, finalURL)
				}
			}
		}

		newBody, _ := json.Marshal(info)
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func handleBaseJs(c *gin.Context) {
	proxy := *embyServer.Proxy
	proxy.ModifyResponse = func(resp *http.Response) error {
		body, _ := utils.ReadBody(resp)
		// 允许混合内容播放 (例如 Https Emby 播放 Http 链接)
		newBody := bytes.ReplaceAll(body, []byte(`mediaSource.IsRemote&&"DirectPlay"===playMethod?null:"anonymous"`), []byte("null"))
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}