package utils

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/andybalholm/brotli"
	"github.com/sirupsen/logrus"
)

// 获取URL的最终目标地址（自动跟踪重定向）
func GetFinalURL(rawURL string, ua string) (string, error) {
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	currentURL := rawURL
	for i := 0; i < 10; i++ {
		req, _ := http.NewRequest(http.MethodHead, currentURL, nil)
		req.Header.Set("User-Agent", ua)
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, err := resp.Location()
			if err != nil {
				return "", err
			}
			// 处理相对路径重定向
			if !loc.IsAbs() {
				u, _ := url.Parse(currentURL)
				currentURL = u.ResolveReference(loc).String()
			} else {
				currentURL = loc.String()
			}
			continue
		}
		return currentURL, nil
	}
	return rawURL, nil
}

// 读取响应体 (解压)
func ReadBody(rw *http.Response) ([]byte, error) {
	encoding := rw.Header.Get("Content-Encoding")
	var reader io.Reader
	switch encoding {
	case "gzip":
		gr, err := gzip.NewReader(rw.Body)
		if err != nil {
			return nil, err
		}
		defer gr.Close()
		reader = gr
	case "br":
		reader = brotli.NewReader(rw.Body)
	default:
		reader = rw.Body
	}
	return io.ReadAll(reader)
}

// 更新响应体 (重新压缩)
func UpdateBody(rw *http.Response, content []byte) error {
	encoding := rw.Header.Get("Content-Encoding")
	var buf bytes.Buffer
	var writer io.Writer

	switch encoding {
	case "gzip":
		gw := gzip.NewWriter(&buf)
		defer gw.Close()
		writer = gw
	case "br":
		bw := brotli.NewWriter(&buf)
		defer bw.Close()
		writer = bw
	default:
		writer = &buf
	}

	if _, err := writer.Write(content); err != nil {
		return err
	}
	// 确保 buffer 刷新
	if c, ok := writer.(io.Closer); ok {
		c.Close()
	}

	rw.Body = io.NopCloser(bytes.NewReader(buf.Bytes()))
	rw.ContentLength = int64(buf.Len())
	rw.Header.Set("Content-Length", strconv.Itoa(buf.Len()))
	return nil
}