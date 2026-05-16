// SPDX-License-Identifier: MPL-2.0
// This Source Code Form is subject to the terms of the Mozilla Public
// License, v. 2.0. If a copy of the MPL was not distributed with this file,
// You can obtain one at https://mozilla.org/MPL/2.0/.

package mieru

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	apicommon "github.com/enfein/mieru/v3/apis/common"
	mieruconstant "github.com/enfein/mieru/v3/apis/constant"
	mierumodel "github.com/enfein/mieru/v3/apis/model"
	mieruserver "github.com/enfein/mieru/v3/apis/server"
	mierutp "github.com/enfein/mieru/v3/apis/trafficpattern"
	mierupb "github.com/enfein/mieru/v3/pkg/appctl/appctlpb"
	log "github.com/sirupsen/logrus"
	"google.golang.org/protobuf/proto"

	"github.com/XrayR-project/XrayR/api"
	"github.com/XrayR-project/XrayR/common/serverstatus"
	"github.com/XrayR-project/XrayR/service/controller"
)

type Service struct {
	apiClient api.API
	config    *controller.Config
	logger    *log.Entry

	mu         sync.RWMutex
	server     mieruserver.Server
	nodeInfo   *api.NodeInfo
	userList   *[]api.UserInfo
	userByName map[string]*userRuntime
	active     sync.Map

	done   chan struct{}
	wg     sync.WaitGroup
	connID atomic.Uint64
}

type userRuntime struct {
	info api.UserInfo
	up   atomic.Int64
	down atomic.Int64
}

type activeConn struct {
	uid int
	ip  string
}

type listenerFactory struct{}

func (listenerFactory) Listen(ctx context.Context, network, address string) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, network, address)
}

func (listenerFactory) ListenPacket(ctx context.Context, network, address string) (net.PacketConn, error) {
	var lc net.ListenConfig
	return lc.ListenPacket(ctx, network, address)
}

type countingWriter struct {
	writer  io.Writer
	counter *atomic.Int64
}

func (w countingWriter) Write(p []byte) (int, error) {
	n, err := w.writer.Write(p)
	if n > 0 {
		w.counter.Add(int64(n))
	}
	return n, err
}

func New(apiClient api.API, config *controller.Config) *Service {
	if config.UpdatePeriodic <= 0 {
		config.UpdatePeriodic = 60
	}
	return &Service{
		apiClient: apiClient,
		config:    config,
		logger: log.NewEntry(log.StandardLogger()).WithFields(log.Fields{
			"Host": apiClient.Describe().APIHost,
			"Type": apiClient.Describe().NodeType,
			"ID":   apiClient.Describe().NodeID,
		}),
		done: make(chan struct{}),
	}
}

func (s *Service) Start() error {
	nodeInfo, err := s.apiClient.GetNodeInfo()
	if err != nil {
		return err
	}
	if nodeInfo.Port == 0 {
		return fmt.Errorf("server port must > 0")
	}
	users, err := s.apiClient.GetUserList()
	if err != nil {
		return err
	}
	if err := s.rebuildServer(nodeInfo, users); err != nil {
		return err
	}
	s.wg.Add(1)
	go s.periodicLoop()
	return nil
}

func (s *Service) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	s.mu.Lock()
	if s.server != nil && s.server.IsRunning() {
		if err := s.server.Stop(); err != nil {
			s.mu.Unlock()
			return err
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	return nil
}

func (s *Service) periodicLoop() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Duration(s.config.UpdatePeriodic) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-s.done:
			return
		case <-ticker.C:
			s.syncAndReport()
		}
	}
}

func (s *Service) syncAndReport() {
	s.reportStatus()
	s.reportTraffic()
	s.reportOnline()

	var (
		newNode  *api.NodeInfo
		newUsers *[]api.UserInfo
		changed  bool
	)

	if nodeInfo, err := s.apiClient.GetNodeInfo(); err != nil {
		if err.Error() != api.NodeNotModified {
			s.logger.Printf("Get node info failed: %s", err)
		}
	} else {
		newNode = nodeInfo
		changed = true
	}

	if users, err := s.apiClient.GetUserList(); err != nil {
		if err.Error() != api.UserNotModified {
			s.logger.Printf("Get user list failed: %s", err)
		}
	} else {
		newUsers = users
		changed = true
	}

	if !changed {
		return
	}

	s.mu.RLock()
	if newNode == nil {
		newNode = s.nodeInfo
	}
	if newUsers == nil {
		newUsers = s.userList
	}
	s.mu.RUnlock()
	if newNode == nil || newUsers == nil {
		return
	}
	if err := s.rebuildServer(newNode, newUsers); err != nil {
		s.logger.Printf("Rebuild Mieru server failed: %s", err)
	}
}

func (s *Service) rebuildServer(nodeInfo *api.NodeInfo, users *[]api.UserInfo) error {
	server, userByName, err := buildServer(nodeInfo, users)
	if err != nil {
		return err
	}

	s.mu.Lock()
	oldServer := s.server
	if oldServer != nil {
		s.server = nil
	}
	s.mu.Unlock()

	if oldServer != nil && oldServer.IsRunning() {
		_ = oldServer.Stop()
	}

	if err := server.Start(); err != nil {
		return err
	}

	s.mu.Lock()
	s.server = server
	s.nodeInfo = nodeInfo
	s.userList = users
	s.userByName = userByName
	s.mu.Unlock()

	s.wg.Add(1)
	go s.acceptLoop(server)
	s.logger.Printf("Mieru server listening on 0.0.0.0:%d/%s with %d users", nodeInfo.Port, nodeInfo.TransportProtocol, len(*users))
	return nil
}

func buildServer(nodeInfo *api.NodeInfo, users *[]api.UserInfo) (mieruserver.Server, map[string]*userRuntime, error) {
	transport := strings.ToUpper(nodeInfo.TransportProtocol)
	if transport == "" {
		transport = "TCP"
	}
	var transportProtocol *mierupb.TransportProtocol
	switch transport {
	case "TCP":
		transportProtocol = mierupb.TransportProtocol_TCP.Enum()
	case "UDP":
		transportProtocol = mierupb.TransportProtocol_UDP.Enum()
	default:
		return nil, nil, fmt.Errorf("unsupported Mieru transport: %s", nodeInfo.TransportProtocol)
	}

	mieruUsers := make([]*mierupb.User, 0, len(*users))
	userByName := make(map[string]*userRuntime, len(*users))
	for _, user := range *users {
		username := user.UUID
		password := user.Passwd
		if password == "" {
			password = user.UUID
		}
		mieruUsers = append(mieruUsers, &mierupb.User{
			Name:     proto.String(username),
			Password: proto.String(password),
		})
		userCopy := user
		userByName[username] = &userRuntime{info: userCopy}
	}
	if len(mieruUsers) == 0 {
		return nil, nil, fmt.Errorf("users is empty")
	}

	var trafficPattern *mierupb.TrafficPattern
	if nodeInfo.MieruTrafficPattern != "" {
		var err error
		trafficPattern, err = mierutp.Decode(nodeInfo.MieruTrafficPattern)
		if err != nil {
			return nil, nil, fmt.Errorf("decode Mieru traffic pattern failed: %w", err)
		}
		if err := mierutp.Validate(trafficPattern); err != nil {
			return nil, nil, fmt.Errorf("invalid Mieru traffic pattern: %w", err)
		}
	}

	serverConfig := &mieruserver.ServerConfig{
		Config: &mierupb.ServerConfig{
			PortBindings: []*mierupb.PortBinding{
				{
					Port:     proto.Int32(int32(nodeInfo.Port)),
					Protocol: transportProtocol,
				},
			},
			Users:          mieruUsers,
			TrafficPattern: trafficPattern,
			AdvancedSettings: &mierupb.ServerAdvancedSettings{
				UserHintIsMandatory: proto.Bool(false),
			},
		},
		StreamListenerFactory: listenerFactory{},
		PacketListenerFactory: listenerFactory{},
	}

	server := mieruserver.NewServer()
	if err := server.Store(serverConfig); err != nil {
		return nil, nil, err
	}
	return server, userByName, nil
}

func (s *Service) acceptLoop(server mieruserver.Server) {
	defer s.wg.Done()
	for {
		conn, req, err := server.Accept()
		if err != nil {
			select {
			case <-s.done:
				return
			default:
			}
			if !server.IsRunning() {
				return
			}
			if isBenignAcceptError(err) {
				s.logger.Warnf("Mieru accept pipe failed, rebuilding server: %s", err)
				s.rebuildCurrentServer(server)
				return
			}
			s.logger.Warnf("Mieru accept failed: %s", err)
			time.Sleep(200 * time.Millisecond)
			continue
		}
		go s.handleConn(conn, req)
	}
}

func (s *Service) rebuildCurrentServer(failedServer mieruserver.Server) {
	s.mu.RLock()
	if s.server != failedServer || s.nodeInfo == nil || s.userList == nil {
		s.mu.RUnlock()
		return
	}
	nodeInfo := s.nodeInfo
	users := s.userList
	s.mu.RUnlock()

	if err := s.rebuildServer(nodeInfo, users); err != nil {
		s.logger.Warnf("Rebuild Mieru server after accept failure failed: %s", err)
	}
}

func isBenignAcceptError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "read/write on closed pipe") ||
		strings.Contains(msg, "use of closed network connection") ||
		strings.Contains(msg, "closed network connection")
}

func (s *Service) handleConn(conn net.Conn, req *mierumodel.Request) {
	uc, ok := conn.(apicommon.UserContext)
	if !ok {
		s.logger.Warnf("Mieru connection from %s has no user context", conn.RemoteAddr())
		_ = conn.Close()
		return
	}
	user := s.getUser(uc.UserName())
	if user == nil {
		s.logger.Warnf("Mieru connection from %s uses unknown user %q", conn.RemoteAddr(), uc.UserName())
		_ = conn.Close()
		return
	}

	sourceIP := remoteIP(conn.RemoteAddr())
	connID := s.connID.Add(1)
	if sourceIP != "" {
		s.active.Store(connID, activeConn{uid: user.info.UID, ip: sourceIP})
		defer s.active.Delete(connID)
	}

	switch req.Command {
	case mieruconstant.Socks5ConnectCmd:
		s.handleTCPConnect(conn, req, user)
	case mieruconstant.Socks5UDPAssociateCmd:
		s.logger.Infof("Mieru UDP associate from uid=%d ip=%s", user.info.UID, sourceIP)
		s.handleUDPAssociate(conn, user)
	default:
		s.logger.Warnf("Unsupported Mieru command from uid=%d ip=%s: %d", user.info.UID, sourceIP, req.Command)
		_ = conn.Close()
	}
}

func (s *Service) handleTCPConnect(conn net.Conn, req *mierumodel.Request, user *userRuntime) {
	defer conn.Close()
	target := requestAddress(req)
	if target == "" {
		s.logger.Warnf("Mieru TCP connect from uid=%d has empty target: %v", user.info.UID, req)
		_ = writeSocks5Response(conn, mieruconstant.Socks5ReplyAddrTypeNotSupported, emptyBindAddr())
		return
	}

	remote, err := net.DialTimeout("tcp", target, 15*time.Second)
	if err != nil {
		s.logger.Warnf("Mieru TCP connect uid=%d target=%s failed: %s", user.info.UID, target, err)
		_ = writeSocks5Response(conn, socks5ReplyFromDialError(err), emptyBindAddr())
		return
	}
	defer remote.Close()

	resp := &mierumodel.Response{
		Reply:    mieruconstant.Socks5ReplySuccess,
		BindAddr: bindAddrSpec(remote.LocalAddr()),
	}
	if err := resp.WriteToSocks5(conn); err != nil {
		s.logger.Warnf("Mieru TCP connect uid=%d target=%s write response failed: %s", user.info.UID, target, err)
		return
	}
	s.logger.Infof("Mieru TCP connect uid=%d target=%s established", user.info.UID, target)

	done := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(countingWriter{writer: remote, counter: &user.up}, conn)
		closeWrite(remote)
		done <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(countingWriter{writer: conn, counter: &user.down}, remote)
		closeWrite(conn)
		done <- struct{}{}
	}()
	<-done
	<-done
}

func closeWrite(conn net.Conn) {
	type closeWriter interface {
		CloseWrite() error
	}
	if cw, ok := conn.(closeWriter); ok {
		_ = cw.CloseWrite()
	}
}

func writeSocks5Response(conn net.Conn, reply byte, bind mierumodel.AddrSpec) error {
	resp := &mierumodel.Response{
		Reply:    reply,
		BindAddr: bind,
	}
	return resp.WriteToSocks5(conn)
}

func emptyBindAddr() mierumodel.AddrSpec {
	return mierumodel.AddrSpec{IP: net.IPv4zero, Port: 0}
}

func bindAddrSpec(addr net.Addr) mierumodel.AddrSpec {
	if tcpAddr, ok := addr.(*net.TCPAddr); ok {
		ip := tcpAddr.IP
		if ip == nil || ip.IsUnspecified() {
			ip = net.IPv4zero
		}
		return mierumodel.AddrSpec{IP: ip, Port: tcpAddr.Port}
	}
	return emptyBindAddr()
}

func socks5ReplyFromDialError(err error) byte {
	if err == nil {
		return mieruconstant.Socks5ReplySuccess
	}
	errText := strings.ToLower(err.Error())
	switch {
	case strings.Contains(errText, "network is unreachable"):
		return mieruconstant.Socks5ReplyNetworkUnreachable
	case strings.Contains(errText, "connection refused"):
		return mieruconstant.Socks5ReplyConnectionRefused
	case strings.Contains(errText, "no such host"):
		return mieruconstant.Socks5ReplyHostUnreachable
	default:
		return mieruconstant.Socks5ReplyServerFailure
	}
}

func (s *Service) handleUDPAssociate(conn net.Conn, user *userRuntime) {
	defer conn.Close()
	resp := &mierumodel.Response{
		Reply: mieruconstant.Socks5ReplySuccess,
		BindAddr: mierumodel.AddrSpec{
			IP:   net.IPv4zero,
			Port: 0,
		},
	}
	if err := resp.WriteToSocks5(conn); err != nil {
		return
	}

	client := apicommon.NewUDPAssociateWrapper(apicommon.NewPacketOverStreamTunnel(conn))
	remote, err := net.ListenPacket("udp", "")
	if err != nil {
		s.logger.Debugf("Create UDP socket failed: %s", err)
		return
	}
	defer remote.Close()

	done := make(chan struct{}, 2)
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := client.ReadFrom(buf)
			if err != nil {
				break
			}
			if n > 0 {
				user.up.Add(int64(n))
				_, _ = remote.WriteTo(buf[:n], addr)
			}
		}
		done <- struct{}{}
	}()
	go func() {
		buf := make([]byte, 64*1024)
		for {
			n, addr, err := remote.ReadFrom(buf)
			if err != nil {
				break
			}
			if n > 0 {
				user.down.Add(int64(n))
				_, _ = client.WriteTo(buf[:n], addr)
			}
		}
		done <- struct{}{}
	}()
	<-done
	_ = conn.Close()
	_ = remote.Close()
	<-done
}

func (s *Service) getUser(username string) *userRuntime {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.userByName[username]
}

func requestAddress(req *mierumodel.Request) string {
	if req == nil {
		return ""
	}
	host := req.DstAddr.FQDN
	if host == "" && req.DstAddr.IP != nil {
		host = req.DstAddr.IP.String()
	}
	if host == "" || req.DstAddr.Port <= 0 {
		return ""
	}
	return net.JoinHostPort(host, strconv.Itoa(int(req.DstAddr.Port)))
}

func remoteIP(addr net.Addr) string {
	if addr == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(addr.String())
	if err != nil {
		return addr.String()
	}
	return host
}

func (s *Service) reportStatus() {
	cpu, mem, disk, uptime, err := serverstatus.GetSystemInfo()
	if err != nil {
		s.logger.Print(err)
		return
	}
	if err := s.apiClient.ReportNodeStatus(&api.NodeStatus{
		CPU:    cpu,
		Mem:    mem,
		Disk:   disk,
		Uptime: uptime,
	}); err != nil {
		s.logger.Print(err)
	}
}

func (s *Service) reportTraffic() {
	if s.config.DisableUploadTraffic {
		return
	}
	var traffic []api.UserTraffic
	var pending []*userRuntime
	s.mu.RLock()
	for _, user := range s.userByName {
		up := user.up.Swap(0)
		down := user.down.Swap(0)
		if up == 0 && down == 0 {
			continue
		}
		traffic = append(traffic, api.UserTraffic{
			UID:      user.info.UID,
			Email:    user.info.Email,
			Upload:   up,
			Download: down,
		})
		pending = append(pending, user)
	}
	s.mu.RUnlock()
	if len(traffic) == 0 {
		return
	}
	if err := s.apiClient.ReportUserTraffic(&traffic); err != nil {
		for i := range pending {
			pending[i].up.Add(traffic[i].Upload)
			pending[i].down.Add(traffic[i].Download)
		}
		s.logger.Print(err)
	}
}

func (s *Service) reportOnline() {
	users := make(map[int]map[string]struct{})
	s.active.Range(func(_, value any) bool {
		conn := value.(activeConn)
		if conn.uid > 0 && conn.ip != "" {
			if users[conn.uid] == nil {
				users[conn.uid] = make(map[string]struct{})
			}
			users[conn.uid][conn.ip] = struct{}{}
		}
		return true
	})
	if len(users) == 0 {
		return
	}
	online := make([]api.OnlineUser, 0, len(users))
	for uid, ips := range users {
		for ip := range ips {
			online = append(online, api.OnlineUser{UID: uid, IP: ip})
		}
	}
	if err := s.apiClient.ReportNodeOnlineUsers(&online); err != nil {
		s.logger.Print(err)
	}
}
