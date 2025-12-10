package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
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
			itemID := strings.TrimPrefix(ms.ID, "mediasource_")
			itemRes, err := embyServer.QueryItem(itemID)
			if err != nil || len(itemRes.Items) == 0 {
				continue
			}
			itemPath := itemRes.Items[0].Path

			// 检查是否匹配配置
			for _, cfg := range config.Cfg.Emby.HttpStrm {
				if matched, _ := regexp.MatchString(cfg.Match, itemPath); matched && cfg.Enable {
					// 强制开启直连
					info.MediaSources[i].SupportsDirectPlay = true
					info.MediaSources[i].SupportsDirectStream = true
					
					// 如果禁止转码，清空转码相关字段
					if !cfg.TransCode {
						info.MediaSources[i].TranscodingUrl = ""
						info.MediaSources[i].TranscodingContainer = ""
					}
					logrus.Infof("应用 HttpStrm 规则: %s", itemPath)
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
	itemID := strings.TrimPrefix(msID, "mediasource_")

	// 查询 Emby 原始路径
	itemRes, err := embyServer.QueryItem(itemID)
	if err != nil || len(itemRes.Items) == 0 {
		embyServer.Proxy.ServeHTTP(c.Writer, c.Request)
		return
	}
	
	realPath := itemRes.Items[0].Path
	
	// 匹配规则
	for _, cfg := range config.Cfg.Emby.HttpStrm {
		if matched, _ := regexp.MatchString(cfg.Match, realPath); matched && cfg.Enable {
			// 处理 HTTP .strm 文件内容
			// 这里假设 realPath 本身就是 strm 文件的路径，或者在 Emby 数据库里 Path 已经是 http 链接
			// 通常 Emby 中 .strm 的 Path 是本地文件路径，我们需要读取该文件内容获取 URL
			// 但这里简化处理，假设 MediaSource.Path 已经是 Emby 解析出来的 URL
			
			// 如果是 .strm 文件，Emby 返回的 MediaSources[0].Path 通常就是 URL
			targetURL := itemRes.Items[0].MediaSources[0].Path

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

			logrus.Infof("重定向播放: %s", targetURL)
			c.Redirect(http.StatusFound, targetURL)
			return
		}
	}

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