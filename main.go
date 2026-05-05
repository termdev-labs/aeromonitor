package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"time"
    "net"
    "strings"

	"github.com/gorilla/websocket"
	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/host"
	"github.com/shirou/gopsutil/v3/mem"
	gnet "github.com/shirou/gopsutil/v3/net"
)

var upgrader = websocket.Upgrader{
	// 支持 Nginx 转发后的 WebSocket 连接
	// Token 已经做鉴权，这里不强限制 Origin，避免 Electron / Nginx 场景误伤
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type SystemInfo struct {
	Time      string      `json:"time"`      // UTC ISO 时间
	Timestamp int64       `json:"timestamp"` // 毫秒时间戳
	OS        OSInfo      `json:"os"`
	CPU       CPUInfo     `json:"cpu"`
	Memory    MemoryInfo  `json:"memory"`
	Disk      DiskInfo    `json:"disk"`
	Net       NetworkInfo `json:"net"`
}

type OSInfo struct {
	Hostname string `json:"hostname"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Kernel   string `json:"kernel"`
	Arch     string `json:"arch"`
	Uptime   uint64 `json:"uptime"`
}

type CPUInfo struct {
	Percent float64 `json:"percent"`
}

type MemoryInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type DiskInfo struct {
	Total       uint64  `json:"total"`
	Used        uint64  `json:"used"`
	Free        uint64  `json:"free"`
	UsedPercent float64 `json:"used_percent"`
}

type NetworkInfo struct {
	BytesSentPerSec uint64 `json:"bytes_sent_per_sec"`
	BytesRecvPerSec uint64 `json:"bytes_recv_per_sec"`
}

type NetTotal struct {
	BytesSent uint64
	BytesRecv uint64
}


func initLogger() {
	logFile := getEnv("MONITOR_LOG_FILE", "monitor.log")

	file, err := os.OpenFile(logFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Println("open log file error:", err)
		return
	}

	// 同时输出到控制台和文件
	log.SetOutput(file)

	// 增加日期、时间、文件行号，方便排查问题
	log.SetFlags(log.LstdFlags | log.Lshortfile)
}


func getClientIP(r *http.Request) string {
	// Nginx 配置了：proxy_set_header X-Real-IP $remote_addr;
	ip := r.Header.Get("X-Real-IP")
	if ip != "" {
		return ip
	}

	// 兼容多级代理
	forwarded := r.Header.Get("X-Forwarded-For")
	if forwarded != "" {
		return strings.TrimSpace(strings.Split(forwarded, ",")[0])
	}

	// fallback：直连时使用 RemoteAddr
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}



func main() {
    initLogger()
	http.HandleFunc("/ws/system", handleSystemWS)

	listenIP := getEnv("MONITOR_LISTEN_IP", "127.0.0.1")
	listenPort := getEnv("MONITOR_LISTEN_PORT", "8000")
	addr := listenIP + ":" + listenPort

	log.Println("server started:", addr)
	log.Println("websocket path: ws://" + addr + "/ws/system?token=YOUR_TOKEN")

	if err := http.ListenAndServe(addr, nil); err != nil {
		log.Fatal(err)
	}
}

func handleSystemWS(w http.ResponseWriter, r *http.Request) {
	if !checkToken(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println("websocket upgrade error:", err)
		return
	}
	defer conn.Close()

	log.Println("client connected:", getClientIP(r))

	// 初始化网卡统计，用于计算每秒上传/下载速度
	lastNet, err := getNetTotal()
	if err != nil {
		log.Println("get net init error:", err)
		return
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for range ticker.C {
		info, err := collectSystemInfo(&lastNet)
		if err != nil {
			log.Println("collect system info error:", err)
			continue
		}

		data, err := json.Marshal(info)
		if err != nil {
			log.Println("json marshal error:", err)
			continue
		}

		if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
			log.Println("client disconnected:", getClientIP(r))
			return
		}
	}
}

func collectSystemInfo(lastNet *NetTotal) (*SystemInfo, error) {
	now := time.Now().UTC()

	cpuPercent, err := cpu.Percent(0, false)
	if err != nil {
		return nil, err
	}

	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}

	du, err := disk.Usage("/")
	if err != nil {
		return nil, err
	}

	hostInfo, err := host.Info()
	if err != nil {
		return nil, err
	}

	currentNet, err := getNetTotal()
	if err != nil {
		return nil, err
	}

	netInfo := NetworkInfo{
		BytesSentPerSec: safeSub(currentNet.BytesSent, lastNet.BytesSent),
		BytesRecvPerSec: safeSub(currentNet.BytesRecv, lastNet.BytesRecv),
	}

	// 更新上一次网卡数据
	*lastNet = currentNet

	cpuValue := 0.0
	if len(cpuPercent) > 0 {
		cpuValue = cpuPercent[0]
	}

	return &SystemInfo{
		Time:      now.Format(time.RFC3339),
		Timestamp: now.UnixMilli(),
		OS: OSInfo{
			Hostname: hostInfo.Hostname,
			Name:     hostInfo.Platform,
			Version:  hostInfo.PlatformVersion,
			Kernel:   hostInfo.KernelVersion,
			Arch:     hostInfo.KernelArch,
			Uptime:   hostInfo.Uptime,
		},
		CPU: CPUInfo{
			Percent: cpuValue,
		},
		Memory: MemoryInfo{
			Total:       vm.Total,
			Used:        vm.Used,
			Free:        vm.Available,
			UsedPercent: vm.UsedPercent,
		},
		Disk: DiskInfo{
			Total:       du.Total,
			Used:        du.Used,
			Free:        du.Free,
			UsedPercent: du.UsedPercent,
		},
		Net: netInfo,
	}, nil
}

func getNetTotal() (NetTotal, error) {
	counters, err := gnet.IOCounters(false)
	if err != nil {
		return NetTotal{}, err
	}

	if len(counters) == 0 {
		return NetTotal{}, nil
	}

	return NetTotal{
		BytesSent: counters[0].BytesSent,
		BytesRecv: counters[0].BytesRecv,
	}, nil
}

func checkToken(r *http.Request) bool {
	serverToken := os.Getenv("MONITOR_TOKEN")

	// 没有设置 token 时，直接拒绝连接，避免裸奔
	if serverToken == "" {
		log.Println("MONITOR_TOKEN is empty")
		return false
	}

	// 支持 ws://host/ws/system?token=xxx
	clientToken := r.URL.Query().Get("token")

	// 也支持 Header: Authorization: Bearer xxx
	if clientToken == "" {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if len(auth) > len(prefix) && auth[:len(prefix)] == prefix {
			clientToken = auth[len(prefix):]
		}
	}

	return clientToken == serverToken
}

func getEnv(key string, defaultValue string) string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return value
}

func safeSub(current uint64, last uint64) uint64 {
	if current < last {
		return 0
	}
	return current - last
}