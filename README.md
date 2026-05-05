#编译
go build 

#运行
export MONITOR_TOKE=xxxx
export MONITOR_LISTEN_IP=127.0.0.1

#可选参数
export MONITOR_LISTEN_PORT=8000

#启动
./monitor

#连接方式

方式1：URL Query Token
ws://127.0.0.1:8000/ws/system?token=YOUR_TOKEN

方式2：Authorization Bearer Token
WebSocket 地址：
ws://127.0.0.1:8000/ws/system

请求头：
Authorization: Bearer YOUR_TOKEN
