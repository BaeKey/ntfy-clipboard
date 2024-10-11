package main

import (
	"encoding/json"
	"fmt"
	"log"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"
	"time"
	"syscall"
	"os"

	"github.com/gorilla/websocket"
	"github.com/atotto/clipboard"
	"github.com/robotn/gohook"
	"github.com/getlantern/systray"
)

// 配置文件名称
const configFileName = "config.json"

// 默认配置
var defaultConfig = Config{
	ClientName: "Windows",
	URLBase:    "ntfy.sh",
	TOPIC:      "hello",
	Token:      "",
	Hotkeys:    "ctrl,shift,x",
}

// 配置文件
type Config struct {
	ClientName string `json:"client_name"`
	URLBase    string `json:"url_base"`
	TOPIC      string `json:"url_topic"`
	Token      string `json:"token"`
	Hotkeys    string `json:"hotkeys"`
}

// 接收消息
type Message struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

// 全局变量
var (
	config   Config
	lastMsg  string
	wsConn   *websocket.Conn
)

// 载入配置
func loadConfig() (Config, error) {
	if _, err := os.Stat(configFileName); os.IsNotExist(err) {
		log.Println("配置文件不存在，创建默认配置文件")
		return saveConfig(defaultConfig)
	}

	data, err := ioutil.ReadFile(configFileName)
	if err != nil {
		return Config{}, err
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, err
	}
	log.Println("配置文件读取成功")
	return cfg, nil
}

// 写入配置文件
func saveConfig(cfg Config) (Config, error) {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return cfg, err
	}
	if err := ioutil.WriteFile(configFileName, data, 0644); err != nil {
		return cfg, err
	}
	log.Println("配置文件写入成功")
	return cfg, nil
}

// 程序入口
func main() {
	var err error
	config, err = loadConfig() // 加载配置文件
	if err != nil {
		log.Fatalf("加载配置时出错: %v", err)
	}

	// 连接WSS
	go wssConnect()

	// 启动键盘监听的 goroutine
	go func() {
		hotkeys := strings.Split(config.Hotkeys, ",")
		hook.Register(hook.KeyDown, hotkeys, func(e hook.Event) {
			onHotkey()
		})
		s := hook.Start()
		<-hook.Process(s)
	}()

	// 获取控制台窗口句柄并隐藏控制台
	consoleHandle, _ := getConsoleWindow()
	toggleWindowVisibility(consoleHandle, false)

	// 启动系统托盘
	systray.Run(onReady, onExit)
}

func onReady() {
	systray.SetIcon(Data)
	systray.SetTitle("云剪贴板")
	systray.SetTooltip("右键点击打开菜单！")
	mShow := systray.AddMenuItem("显示", "显示窗口")
	mHide := systray.AddMenuItem("隐藏", "隐藏窗口")
	systray.AddSeparator()
	mQuit := systray.AddMenuItem("退出", "退出程序")

	consoleHandle, err := getConsoleWindow()
	if err != nil {
		log.Println("获取控制台窗口句柄失败:", err)
		return
	}

	go func() {
    // 默认菜单 
	mHide.Disable()
	mShow.Enable()
		for {
			select {
			case <-mShow.ClickedCh:
				toggleWindowVisibility(consoleHandle, true)
				mShow.Disable()
				mHide.Enable()
			case <-mHide.ClickedCh:
				toggleWindowVisibility(consoleHandle, false)
				mHide.Disable()
				mShow.Enable()
			case <-mQuit.ClickedCh:
				systray.Quit()
			}
		}
	}()
}

// 退出
func onExit() {
	if wsConn != nil {
		if err := wsConn.Close(); err != nil {
			log.Printf("关闭 WebSocket 出错: %v", err)
		} else {
			log.Println("WebSocket 连接已成功关闭")
		}
	}
	hook.End()
}

// 解析消息
func onMessage(message []byte) {
	var msg Message
	if err := json.Unmarshal(message, &msg); err != nil {
		log.Printf("消息解析失败： %v", err)
		return
	}

	if msg.Title == config.ClientName || msg.Message == "" || msg.Message == lastMsg {
		return
	}

	lastMsg = msg.Message
	if err := clipboard.WriteAll(msg.Message); err != nil {
		log.Printf("写入剪贴板错误： %v", err)
	}
}

// WebSocket连接
func wssConnect() {
	for {
		u := url.URL{Scheme: "wss", Host: config.URLBase, Path: config.TOPIC + "/ws"}
		headers := http.Header{}
		if config.Token != "" {
			headers.Add("Authorization", "Bearer "+config.Token)
		}

		conn, _, err := websocket.DefaultDialer.Dial(u.String(), headers)
		if err != nil {
			log.Println("WebSocket 连接失败，5秒后重试:", err)
			time.Sleep(5 * time.Second)
			continue
		}
		defer conn.Close() // 确保在退出时关闭连接
		log.Println("WebSocket 连接成功，开始监听消息")

		for {
			_, message, err := conn.ReadMessage()
			if err != nil {
				log.Printf("读取消息错误: %v", err)
				break
			}
			onMessage(message)
		}
	}
}

// 发送剪贴板内容
func sendClipboard(msg string) {
	client := &http.Client{}
	req, err := http.NewRequest("PUT", fmt.Sprintf("https://%s/%s", config.URLBase, config.TOPIC), strings.NewReader(msg))
	if err != nil {
		log.Printf("创建请求错误: %v", err)
		return
	}
	if config.Token != "" {
		req.Header.Add("Authorization", "Bearer "+config.Token)
	}
	req.Header.Add("Title", config.ClientName)

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("发送剪贴板内容错误: %v", err)
		return
	}
	defer resp.Body.Close()
}

// 快捷键按下后触发
func onHotkey() {
	clipboardContent, _ := clipboard.ReadAll()
	if clipboardContent != "" {
		sendClipboard(clipboardContent)
	}
}

// 获取控制台窗口句柄
func getConsoleWindow() (syscall.Handle, error) {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getConsoleWindows := kernel32.NewProc("GetConsoleWindow")
	handle, _, _ := getConsoleWindows.Call()
	if handle == 0 {
		return 0, fmt.Errorf("无法获取控制台窗口句柄")
	}
	return syscall.Handle(handle), nil
}

// 切换窗口可见性
func toggleWindowVisibility(consoleHandle syscall.Handle, show bool) {
	user32 := syscall.NewLazyDLL("user32.dll")
	showWindowAsync := user32.NewProc("ShowWindowAsync")
	if show {
		showWindowAsync.Call(uintptr(consoleHandle), 5)
	} else {
		showWindowAsync.Call(uintptr(consoleHandle), 0)
	}
}
