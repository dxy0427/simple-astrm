package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
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
	regexStream       = regexp.MustCompile(`(?i)/Videos/\d+/(stream|original)`)
	regexBaseJs       = regexp.MustCompile(`(?i)basehtmlplayer.js`)
)

func Init(r *gin.Engine) {
	embyServer = service.NewEmbyServer()
	r.NoRoute(Handler)
}

func Handler(c *gin.Context) {
	path := c.Request.URL.Path

	// 1. 修改 PlaybackInfo (强制直连，并接管流地址)
	if regexPlaybackInfo.MatchString(path) {
		handlePlaybackInfo(c)
		return
	}

	// 2. 拦截视频流 (执行 302 重定向)
	if regexStream.MatchString(path) {
		handleStream(c)
		return
	}

	// 3. 修改 JS (解决跨域混排)
	if regexBaseJs.MatchString(path) {
		handleBaseJs(c)
		return
	}

	// 默认代理
	embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
}

func handlePlaybackInfo(c *gin.Context) {
	// 创建一个新的 Proxy 实例来拦截响应
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

		// 获取 URL 前缀 (例如 /emby)
		prefix := ""
		if strings.HasPrefix(c.Request.URL.Path, "/emby") {
			prefix = "/emby"
		}

		// 准备鉴权参数，用于构造 DirectStreamUrl
		queryParams := c.Request.URL.Query()
		authParams := url.Values{}
		// 常用鉴权字段，确保重定向回来的请求能通过认证
		keys := []string{"api_key", "X-Emby-Token", "X-Emby-Client", "X-Emby-Device-Id", "X-Emby-Device-Name", "X-Emby-Client-Version", "X-Emby-Language"}
		for _, k := range keys {
			if v := queryParams.Get(k); v != "" {
				authParams.Set(k, v)
			} else if v := c.GetHeader(k); v != "" {
				authParams.Set(k, v)
			}
		}

		// 遍历媒体源
		for i, ms := range info.MediaSources {
			itemID := strings.TrimPrefix(ms.ID, "mediasource_")
			
			// 查询 Item 获取物理路径用于匹配规则
			itemRes, err := embyServer.QueryItem(itemID)
			if err != nil || len(itemRes.Items) == 0 {
				continue
			}
			itemPath := itemRes.Items[0].Path

			// 检查是否匹配配置
			for _, cfg := range config.Cfg.Emby.HttpStrm {
				if matched, _ := regexp.MatchString(cfg.Match, itemPath); matched && cfg.Enable {
					// 仅处理 Protocol 为 Http/Https 的源 (即 Strm 内容为链接)
					if ms.Protocol == "Http" || ms.Protocol == "Https" {
						// 1. 开启直连权限
						info.MediaSources[i].SupportsDirectPlay = true
						info.MediaSources[i].SupportsDirectStream = true

						// 2. 清空转码参数 (如果配置了不转码)
						if !cfg.TransCode {
							info.MediaSources[i].TranscodingUrl = ""
							info.MediaSources[i].TranscodingContainer = ""
							info.MediaSources[i].SupportsTranscoding = false
						}

						// 3. [关键修复] 篡改 DirectStreamUrl
						// 将直连地址指向本代理服务器的 /Videos/.../stream 接口
						// 这样客户端会请求代理，代理再在 handleStream 中做 302 跳转
						authParams.Set("MediaSourceId", ms.ID)
						authParams.Set("Static", "true")
						
						// 构造代理链接: /emby/Videos/{ItemID}/stream?MediaSourceId=...&api_key=...
						fakeStreamUrl := fmt.Sprintf("%s/Videos/%s/stream?%s", prefix, itemID, authParams.Encode())
						
						info.MediaSources[i].DirectStreamUrl = fakeStreamUrl

						logrus.Infof("应用规则: %s | 拦截原链接: %s | 伪装代理链接: %s", itemPath, ms.Path, fakeStreamUrl)
					}
				}
			}
		}

		newBody, _ := json.Marshal(info)
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

func handleStream(c *gin.Context) {
	// 获取 MediaSourceId
	msID := c.Query("MediaSourceId")
	if msID == "" {
		msID = c.Query("mediasourceid")
	}
	cleanID := strings.TrimPrefix(msID, "mediasource_")

	// 从 URL 提取 ItemID (/Videos/{ItemID}/stream)
	urlParts := strings.Split(c.Request.URL.Path, "/")
	var itemID string
	for i, part := range urlParts {
		if strings.EqualFold(part, "Videos") && i+1 < len(urlParts) {
			itemID = urlParts[i+1]
			break
		}
	}

	// 查询 Emby 原始 Item 信息
	itemRes, err := embyServer.QueryItem(itemID)
	if err != nil || len(itemRes.Items) == 0 {
		embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
		return
	}
	
	realPath := itemRes.Items[0].Path
	
	// 匹配规则
	for _, cfg := range config.Cfg.Emby.HttpStrm {
		if matched, _ := regexp.MatchString(cfg.Match, realPath); matched && cfg.Enable {
			
			// 寻找匹配当前请求 ID 的 MediaSource
			var targetMediaSource *service.MediaSource
			for _, ms := range itemRes.Items[0].MediaSources {
				if strings.TrimPrefix(ms.ID, "mediasource_") == cleanID {
					targetMediaSource = &ms
					break
				}
			}
			// 容错：如果只有一个源，默认就是它
			if targetMediaSource == nil && len(itemRes.Items[0].MediaSources) == 1 {
				targetMediaSource = &itemRes.Items[0].MediaSources[0]
			}

			if targetMediaSource != nil && (targetMediaSource.Protocol == "Http" || targetMediaSource.Protocol == "Https") {
				targetURL := targetMediaSource.Path // 这是 strm 里的原始链接 (如 http://127.0.0.1:5244/...)

				// 执行 URL 替换 (actions)
				for _, action := range cfg.Actions {
					if action.Type == "replace" {
						parts := strings.Split(action.Args, "->")
						if len(parts) == 2 {
							oldVal := strings.TrimSpace(parts[0])
							newVal := strings.TrimSpace(parts[1])
							// 这里会把 127.0.0.1 替换成你配置的域名
							targetURL = strings.ReplaceAll(targetURL, oldVal, newVal)
						}
					}
				}

				// 获取最终 URL (FinalURL)
				if cfg.FinalURL {
					if final, err := utils.GetFinalURL(targetURL, c.Request.UserAgent()); err == nil {
						targetURL = final
					}
				}

				logrus.Infof("重定向播放 [302]: %s", targetURL)
				c.Redirect(http.StatusFound, targetURL)
				return
			}
		}
	}

	// 没匹配到或无法处理，走默认代理
	embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
}

func handleBaseJs(c *gin.Context) {
	proxy := *embyServer.Proxy
	proxy.ModifyResponse = func(resp *http.Response) error {
		body, _ := utils.ReadBody(resp)
		// 允许混合内容播放
		newBody := bytes.ReplaceAll(body, []byte(`mediaSource.IsRemote&&"DirectPlay"===playMethod?null:"anonymous"`), []byte("null"))
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}