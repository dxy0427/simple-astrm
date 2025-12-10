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
	// 稍微放宽匹配，确保能抓到 /emby/Videos/...
	regexStream       = regexp.MustCompile(`(?i)/Videos/(.*)/(stream|original)`)
	regexBaseJs       = regexp.MustCompile(`(?i)basehtmlplayer.js`)
)

func Init(r *gin.Engine) {
	embyServer = service.NewEmbyServer()
	r.NoRoute(Handler)
}

func Handler(c *gin.Context) {
	path := c.Request.URL.Path

	// 1. 修改 PlaybackInfo (强制直连 + 伪装链接)
	if regexPlaybackInfo.MatchString(path) {
		handlePlaybackInfo(c)
		return
	}

	// 2. 拦截视频流 (解析真实链接 + 302重定向)
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

// 处理 PlaybackInfo：告诉客户端“可以直连”，但给它一个“假”的直连地址（指向本代理）
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

		// 收集鉴权参数，拼接到伪装链接后面，防止重定向回来时 401
		queryParams := c.Request.URL.Query()
		authParams := url.Values{}
		keys := []string{"api_key", "X-Emby-Token", "X-Emby-Client", "X-Emby-Device-Id", "X-Emby-Device-Name", "X-Emby-Client-Version", "X-Emby-Language"}
		for _, k := range keys {
			if v := queryParams.Get(k); v != "" {
				authParams.Set(k, v)
			} else if v := c.GetHeader(k); v != "" {
				authParams.Set(k, v)
			}
		}

		// 确定 API 前缀 (适应 /emby 路径或根路径)
		urlPrefix := ""
		if strings.HasPrefix(c.Request.URL.Path, "/emby") {
			urlPrefix = "/emby"
		}

		for i, ms := range info.MediaSources {
			if ms.ID == nil {
				continue
			}
			// ID 处理 (兼容 emby 4.9+)
			itemID := strings.TrimPrefix(*ms.ID, "mediasource_")

			// 查询 Item 物理路径用于匹配规则
			itemRes, err := embyServer.QueryItem(itemID)
			if err != nil || len(itemRes.Items) == 0 || itemRes.Items[0].Path == nil {
				continue
			}
			itemPath := *itemRes.Items[0].Path

			// 规则匹配
			for _, cfg := range config.Cfg.Emby.HttpStrm {
				if matched, _ := regexp.MatchString(cfg.Match, itemPath); matched && cfg.Enable {
					// 仅处理 Strm (Protocol 通常是 Http/Https)
					if ms.Protocol != nil && (*ms.Protocol == "Http" || *ms.Protocol == "Https") {
						
						// 1. 强制允许直连
						info.MediaSources[i].SupportsDirectPlay = service.BoolPtr(true)
						info.MediaSources[i].SupportsDirectStream = service.BoolPtr(true)

						// 2. 如果禁止转码，清空相关字段
						if !cfg.TransCode {
							info.MediaSources[i].SupportsTranscoding = service.BoolPtr(false)
							info.MediaSources[i].TranscodingUrl = nil
							info.MediaSources[i].TranscodingContainer = nil
							info.MediaSources[i].TranscodingSubProtocol = nil
						}

						// 3. [关键] 构造伪装的 DirectStreamUrl
						// 格式: /emby/Videos/{ItemID}/stream?MediaSourceId={MSID}&Static=true&...
						authParams.Set("MediaSourceId", *ms.ID)
						authParams.Set("Static", "true")
						
						// 这里的 VideoId 使用 URL 路径中的 ItemId，通常 ms.ItemId 就是它
						vid := itemID
						if ms.ItemID != nil {
							vid = *ms.ItemID
						}

						fakeUrl := fmt.Sprintf("%s/Videos/%s/stream?%s", urlPrefix, vid, authParams.Encode())
						info.MediaSources[i].DirectStreamUrl = service.StrPtr(fakeUrl)

						logrus.Infof("规则匹配: %s | 伪装直连地址 -> %s", itemPath, fakeUrl)
					}
				}
			}
		}

		newBody, _ := json.Marshal(info)
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// 处理 Stream：拦截伪装链接，重定向到真实链接
func handleStream(c *gin.Context) {
	// 获取 MediaSourceId
	msID := c.Query("MediaSourceId")
	if msID == "" {
		msID = c.Query("mediasourceid")
	}
	cleanID := strings.TrimPrefix(msID, "mediasource_")

	// 从 URL 提取 ItemID (/Videos/{ItemID}/stream)
	// regex: /Videos/(.*)/(stream|original)
	matches := regexStream.FindStringSubmatch(c.Request.URL.Path)
	itemID := ""
	if len(matches) >= 2 {
		itemID = matches[1]
	}

	// 查询 Item
	itemRes, err := embyServer.QueryItem(itemID)
	if err != nil || len(itemRes.Items) == 0 || itemRes.Items[0].Path == nil {
		embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
		return
	}

	realPath := *itemRes.Items[0].Path

	// 规则匹配
	for _, cfg := range config.Cfg.Emby.HttpStrm {
		if matched, _ := regexp.MatchString(cfg.Match, realPath); matched && cfg.Enable {
			
			// 找到对应的 MediaSource
			var targetMS *service.MediaSourceInfo
			for _, ms := range itemRes.Items[0].MediaSources {
				if ms.ID != nil && strings.TrimPrefix(*ms.ID, "mediasource_") == cleanID {
					targetMS = &ms
					break
				}
			}
			// 容错：如果没找到 ID 且只有一个源，默认就是它
			if targetMS == nil && len(itemRes.Items[0].MediaSources) == 1 {
				targetMS = &itemRes.Items[0].MediaSources[0]
			}

			// 如果是 Http 协议的 Strm
			if targetMS != nil && targetMS.Protocol != nil && (*targetMS.Protocol == "Http" || *targetMS.Protocol == "Https") {
				// 获取 Strm 里的真实链接
				if targetMS.Path == nil {
					break 
				}
				finalURL := *targetMS.Path

				// 执行替换 (例如把 127.0.0.1 换成 公网IP)
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

				// 获取最终重定向 URL
				if cfg.FinalURL {
					if u, err := utils.GetFinalURL(finalURL, c.Request.UserAgent()); err == nil {
						finalURL = u
					}
				}

				logrus.Infof("HttpStrm 重定向至: %s", finalURL)
				c.Redirect(http.StatusFound, finalURL)
				return
			}
		}
	}

	// 没匹配到规则或不是 Strm，走 Emby 默认流
	embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
}

func handleBaseJs(c *gin.Context) {
	proxy := *embyServer.Proxy
	proxy.ModifyResponse = func(resp *http.Response) error {
		body, _ := utils.ReadBody(resp)
		newBody := bytes.ReplaceAll(body, []byte(`mediaSource.IsRemote&&"DirectPlay"===playMethod?null:"anonymous"`), []byte("null"))
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}