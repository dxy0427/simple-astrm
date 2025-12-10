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

// 拦截 PlaybackInfo，注入伪装的代理链接
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

		// 准备鉴权参数
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

		urlPrefix := ""
		if strings.HasPrefix(c.Request.URL.Path, "/emby") {
			urlPrefix = "/emby"
		}

		cfg := config.Cfg.Emby.HttpStrm

		for i, ms := range info.MediaSources {
			// 简化判断：只要 Emby 识别为 Http/Https 协议，就视为 Strm/远程流，应用全局配置
			// 本地文件的 Protocol 通常是 "File"
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

				// 3. 构造伪装链接
				authParams.Set("MediaSourceId", *ms.ID)
				authParams.Set("Static", "true")
				
				// 获取 ItemID
				vid := "0"
				if ms.ID != nil {
					// 尝试从 mediasource_xxx 中提取 ID
					vid = strings.TrimPrefix(*ms.ID, "mediasource_")
				}
				if ms.ItemID != nil {
					vid = *ms.ItemID
				}

				fakeUrl := fmt.Sprintf("%s/Videos/%s/stream?%s", urlPrefix, vid, authParams.Encode())
				info.MediaSources[i].DirectStreamUrl = service.StrPtr(fakeUrl)

				// 打印日志 (Path 通常包含 strm 文件路径或 URL)
				logPath := "unknown"
				if ms.Path != nil { logPath = *ms.Path }
				logrus.Infof("应用全局规则 | 原始资源: %s | 伪装链接 -> %s", logPath, fakeUrl)
			}
		}

		newBody, _ := json.Marshal(info)
		return utils.UpdateBody(resp, newBody)
	}
	proxy.ServeHTTP(c.Writer, c.Request)
}

// 拦截 Stream 请求，提取真实链接并 302
func handleStream(c *gin.Context) {
	// 获取 MediaSourceId
	msID := c.Query("MediaSourceId")
	if msID == "" {
		msID = c.Query("mediasourceid")
	}
	cleanID := strings.TrimPrefix(msID, "mediasource_")

	// 提取 ItemID
	matches := regexStream.FindStringSubmatch(c.Request.URL.Path)
	itemID := ""
	if len(matches) >= 2 {
		itemID = matches[1]
	}

	// 查询 Item
	itemRes, err := embyServer.QueryItem(itemID)
	if err != nil || len(itemRes.Items) == 0 {
		embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
		return
	}

	cfg := config.Cfg.Emby.HttpStrm

	// 查找对应的 MediaSource
	var targetMS *service.MediaSourceInfo
	for _, ms := range itemRes.Items[0].MediaSources {
		if ms.ID != nil && strings.TrimPrefix(*ms.ID, "mediasource_") == cleanID {
			targetMS = &ms
			break
		}
	}
	// 容错
	if targetMS == nil && len(itemRes.Items[0].MediaSources) == 1 {
		targetMS = &itemRes.Items[0].MediaSources[0]
	}

	// 如果是 HTTP/HTTPS 协议，应用全局逻辑
	if targetMS != nil && targetMS.Protocol != nil && (*targetMS.Protocol == "Http" || *targetMS.Protocol == "Https") {
		if targetMS.Path == nil {
			embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
			return
		}
		
		finalURL := *targetMS.Path

		// 执行 Actions 替换
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

		// 获取最终 URL (FinalURL)
		if cfg.FinalURL {
			if u, err := utils.GetFinalURL(finalURL, c.Request.UserAgent()); err == nil {
				finalURL = u
			}
		}

		logrus.Infof("302 重定向 -> %s", finalURL)
		c.Redirect(http.StatusFound, finalURL)
		return
	}

	// 本地文件，默认转发
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