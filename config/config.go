package config

import (
	"os"

	"gopkg.in/yaml.v3"
)

// 全局配置实例
var Cfg *Config

type Action struct {
	Type string `yaml:"type"`
	Args string `yaml:"args"`
}

// HttpStrm 简化为单对象
type HttpStrm struct {
	Actions   []Action `yaml:"actions"`
	TransCode bool     `yaml:"transCode"`
	FinalURL  bool     `yaml:"finalURL"`
}

type EmbyConfig struct {
	Addr     string   `yaml:"addr"`
	ApiKey   string   `yaml:"apiKey"`
	HttpStrm HttpStrm `yaml:"httpStrm"` // 修改为单结构体，非数组
}

type Config struct {
	Listen string     `yaml:"listen"`
	Emby   EmbyConfig `yaml:"emby"`
}

func Init(path string) {
	Cfg = &Config{}
	bytes, err := os.ReadFile(path)
	if err != nil {
		panic("无法读取配置文件: " + err.Error())
	}
	if err := yaml.Unmarshal(bytes, Cfg); err != nil {
		panic("解析配置文件失败: " + err.Error())
	}
}