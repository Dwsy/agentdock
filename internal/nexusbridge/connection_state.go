package nexusbridge

import "sync/atomic"

// ConnectionState 只记录当前节点桥接是否已完成 NexusDock 握手以及对端服务标识。
// 配置是否存在由启动配置判断，避免把“已配对”和“已连接”混成同一个状态。
type ConnectionState struct {
	connected atomic.Bool
	server    atomic.Value
}

func (s *ConnectionState) SetConnected(connected bool) {
	s.connected.Store(connected)
	if !connected {
		s.server.Store(ServerInfo{})
	}
}

// SetConnectedServer 在握手完成后原子记录已连接的 Nexus 服务端版本标识。
func (s *ConnectionState) SetConnectedServer(info ServerInfo) {
	s.server.Store(info.normalize())
	s.connected.Store(true)
}

func (s *ConnectionState) Connected() bool {
	return s.connected.Load()
}

// Server 返回当前活跃连接对端的版本与协议标识；未连接时返回零值。
func (s *ConnectionState) Server() ServerInfo {
	if !s.Connected() {
		return ServerInfo{}
	}
	v := s.server.Load()
	if info, ok := v.(ServerInfo); ok {
		return info
	}
	return ServerInfo{}
}

// IsWorkers 报告当前活跃连接是否指向 Cloudflare Workers 版 NexusDock。
func (s *ConnectionState) IsWorkers() bool {
	return s.Connected() && s.Server().IsWorkers()
}
