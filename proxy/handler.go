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
	regexStream       = regexp.MustCompile(`(?i)/Videos/\d+/(stream|original)`)
	regexBaseJs       = regexp.MustCompile(`(?i)basehtmlplayer.js`)
)

func Init(r *gin.Engine) {
	embyServer = service.NewEmbyServer()
	r.NoRoute(Handler)
}

func Handler(c *gin.Context) {
	path := c.Request.URL.Path

	// 1. 修改 PlaybackInfo (强制直连)
	if regexPlaybackInfo.MatchString(path) {
		handlePlaybackInfo(c)
		return
	}

	// 2. 拦截视频流 (重定向)
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

		// 遍历媒体源，匹配 HttpStrm 规则
		for i, ms := range info.MediaSources {
			// 获取对应的 Item 信息
			// Emby 的 ID 可能是纯数字，也可能带有 mediasource_ 前缀
			itemID := strings.TrimPrefix(ms.ID, "mediasource_")
			
			// 注意：PlaybackInfo 的 URL 中已经包含了 ItemId，其实可以直接用，
			// 但为了获取 Path (文件名) 来匹配规则，我们通常还是需要查一下 Item 详情，
			// 或者如果 info.MediaSources[i].Path 已经是本地路径，直接匹配也可。
			// 这里为了稳妥，我们查询一次 Item 获取其物理路径用于规则匹配。
			itemRes, err := embyServer.QueryItem(itemID)
			if err != nil || len(itemRes.Items) == 0 {
				continue
			}
			itemPath := itemRes.Items[0].Path

			// 检查是否匹配配置
			for _, cfg := range config.Cfg.Emby.HttpStrm {
				if matched, _ := regexp.MatchString(cfg.Match, itemPath); matched && cfg.Enable {
					// 只有当 Protocol 是 Http/Https 时，才代表 Emby 识别到了 strm 内的链接
					// 如果 Protocol 是 File，说明 Emby 把它当本地文件（可能需要挂载），这种情况无法直连
					if ms.Protocol == "Http" || ms.Protocol == "Https" {
						// 强制开启直连
						info.MediaSources[i].SupportsDirectPlay = true
						info.MediaSources[i].SupportsDirectStream = true

						// 如果禁止转码，清空转码相关字段
						if !cfg.TransCode {
							info.MediaSources[i].TranscodingUrl = ""
							info.MediaSources[i].TranscodingContainer = ""
						}
						
						// 这里 ms.Path 就是 strm 文件里的 URL
						logrus.Infof("应用 HttpStrm 规则: %s | 真实链接: %s", itemPath, ms.Path)
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
	// 处理 Emby 可能的 ID 前缀
	cleanID := strings.TrimPrefix(msID, "mediasource_")

	// 查询 Emby 原始 Item 信息
	// 这里的 path regex 提取的 ID 其实是 ItemId，但 Query 参数里的是 MediaSourceId
	// 通常 .strm 文件的 ItemId 和 MediaSourceId 的数字部分是一样的，但也可能不同。
	// 安全起见，我们应该通过 MediaSourceId 反查 Item，或者先查 URL 里的 VideoId。
	// 这里简化逻辑：直接用 VideoId (从 URL 路径获取) 查 Item。
	
	// 从 URL 提取 ItemID: /Videos/{ItemID}/stream
	urlParts := strings.Split(c.Request.URL.Path, "/")
	var itemID string
	for i, part := range urlParts {
		if strings.EqualFold(part, "Videos") && i+1 < len(urlParts) {
			itemID = urlParts[i+1]
			break
		}
	}

	itemRes, err := embyServer.QueryItem(itemID)
	if err != nil || len(itemRes.Items) == 0 {
		embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
		return
	}
	
	// Item 的物理路径 (例如 /mnt/strm/movie.strm)
	realPath := itemRes.Items[0].Path
	
	// 匹配规则
	for _, cfg := range config.Cfg.Emby.HttpStrm {
		if matched, _ := regexp.MatchString(cfg.Match, realPath); matched && cfg.Enable {
			
			// 寻找匹配当前请求 ID 的 MediaSource
			var targetMediaSource *service.MediaSource
			for _, ms := range itemRes.Items[0].MediaSources {
				// ID 比较：忽略 mediasource_ 前缀进行比较
				if strings.TrimPrefix(ms.ID, "mediasource_") == cleanID {
					targetMediaSource = &ms
					break
				}
			}

			// 如果没找到指定 ID 的源，且只有一个源，通常就是它 (容错)
			if targetMediaSource == nil && len(itemRes.Items[0].MediaSources) == 1 {
				targetMediaSource = &itemRes.Items[0].MediaSources[0]
			}

			if targetMediaSource != nil {
				// 关键点：对于无需挂载的 Strm，Emby 会将 Protocol 识别为 Http/Https
				// 此时 Path 字段就是 strm 文件里写的 URL
				if targetMediaSource.Protocol == "Http" || targetMediaSource.Protocol == "Https" {
					targetURL := targetMediaSource.Path

					// 执行 URL 替换 (actions)
					for _, action := range cfg.Actions {
						if action.Type == "replace" {
							parts := strings.Split(action.Args, "->")
							if len(parts) == 2 {
								oldVal := strings.TrimSpace(parts[0])
								newVal := strings.TrimSpace(parts[1])
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

					logrus.Infof("HttpStrm 重定向至: %s", targetURL)
					c.Redirect(http.StatusFound, targetURL)
					return
				} else {
					logrus.Warnf("匹配到规则，但 Protocol 不是 Http (是 %s)，可能 Emby 未能解析 Strm 内容或需要本地挂载", targetMediaSource.Protocol)
				}
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