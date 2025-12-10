package main

import (
	"simple-astrm/config"
	"simple-astrm/proxy"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
)

func main() {
	// 1. 加载配置
	config.Init("config.yaml")

	// 2. 初始化 Gin
	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// 3. 初始化代理路由
	proxy.Init(r)

	// 4. 启动服务
	addr := config.Cfg.Listen
	if addr == "" {
		addr = ":8095"
	}
	logrus.Infof("Simple Astrm 启动成功，监听: %s，代理 Emby: %s", addr, config.Cfg.Emby.Addr)
	r.Run(addr)
}