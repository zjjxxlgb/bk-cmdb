/*
 * Tencent is pleased to support the open source community by making 蓝鲸 available.
 * Copyright (C) 2017-2018 THL A29 Limited, a Tencent company. All rights reserved.
 * Licensed under the MIT License (the "License"); you may not use this file except
 * in compliance with the License. You may obtain a copy of the License at
 * http://opensource.org/licenses/MIT
 * Unless required by applicable law or agreed to in writing, software distributed under
 * the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
 * either express or implied. See the License for the specific language governing permissions and
 * limitations under the License.
 */

package discovery

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"configcenter/src/common/blog"
	"configcenter/src/common/registerdiscover"
	"configcenter/src/common/types"
)

func newServerDiscover(disc *registerdiscover.RegDiscover, path, name string) (*server, error) {
	discoverChan, eventErr := disc.DiscoverService(path)
	if nil != eventErr {
		return nil, eventErr
	}

	svr := &server{
		path:         path,
		name:         name,
		servers:      make([]*types.ServerInfo, 0),
		discoverChan: discoverChan,
		serversChan: make(chan []string, 1),
	}

	svr.run()
	return svr, nil
}

type server struct {
	sync.RWMutex
	index int
	// server's name
	name         string
	path         string
	servers      []*types.ServerInfo
	uuids        []string
	discoverChan <-chan *registerdiscover.DiscoverEvent
	serversChan chan []string
}

func (s *server) GetServers() ([]string, error) {
	if s == nil {
		return []string{}, nil
	}
	s.RLock()
	defer s.RUnlock()

	num := len(s.servers)
	if num == 0 {
		return []string{}, fmt.Errorf("oops, there is no %s can be used", s.name)
	}

	if s.index < num-1 {
		s.index = s.index + 1
		s.servers = append(s.servers[s.index-1:], s.servers[:s.index-1]...)
	} else {
		s.index = 0
		s.servers = append(s.servers[num-1:], s.servers[:num-1]...)
	}

	return s.GetRegAddrs(), nil
}

// IsMaster 判断当前进程是否为master 进程， 服务注册节点的第一个节点
// 注册地址不能作为区分标识，因为不同的机器可能用一样的域名作为注册地址，所以用uuid区分
func (s *server) IsMaster(UUID string) bool {
	if s == nil {
		return false
	}
	s.RLock()
	defer s.RUnlock()
	if 0 < len(s.servers) {
		return s.servers[0].UUID == UUID
	}
	return false

}

func (s *server) run() {
	blog.Infof("start to discover cc component from zk, path:[%s].", s.path)
	go func() {
		for svr := range s.discoverChan {
			blog.Warnf("received one zk event from path %s.", s.path)
			if svr.Err != nil {
				blog.Errorf("get zk event with error about path[%s]. err: %v", s.path, svr.Err)
				continue
			}

			if len(svr.Server) <= 0 {
				blog.Warnf("get zk event with 0 instance with path[%s], reset its servers", s.path)
				s.resetServer()
				s.setServersChan()
				continue
			}

			s.updateServer(svr.Server)
			s.setServersChan()
		}
	}()
}

func (s *server) resetServer() {
	s.Lock()
	defer s.Unlock()
	s.servers = make([]*types.ServerInfo, 0)
}

// 当监听到服务节点变化时，将最新的服务节点信息放入该channel里
func (s *server) setServersChan() {
	// 即使没有其他服务消费该channel，也能保证该channel不会阻塞
	for len(s.serversChan) >=1 {
		<- s.serversChan
	}
	s.serversChan <- s.getInstances()
}

// 获取zk上最新的服务节点信息channel
func (s *server) GetServersChan() chan []string {
	return s.serversChan
}

// 获取所有注册服务节点的ip:port
func (s *server) getInstances() []string {
	addrArr := []string{}
	s.RLock()
	defer s.RUnlock()
	for _, info := range s.servers {
		addrArr = append(addrArr, info.Instance())
	}
	return addrArr
}

func (s *server) updateServer(svrs []string) {
	servers := make([]*types.ServerInfo, 0)

	for _, svr := range svrs {
		server := new(types.ServerInfo)
		if err := json.Unmarshal([]byte(svr), server); err != nil {
			blog.Errorf("unmarshal server info failed, zk path[%s], err: %v", s.path, err)
			continue
		}

		if server.Scheme != "https" {
			server.Scheme = "http"
		}

		if server.Port == 0 {
			blog.Errorf("invalid port 0, with zk path: %s", s.path)
			continue
		}

		if len(server.RegisterIP) == 0 {
			blog.Errorf("invalid ip with zk path: %s", s.path)
			continue
		}

		servers = append(servers, server)

	}

	s.Lock()
	defer s.Unlock()

	if len(servers) != 0 {
		s.servers = servers
		regAddrs := s.GetRegAddrs()
		blog.V(5).Infof("update component with new server instance[%s] about path: %s", strings.Join(regAddrs, "; "), s.path)
	}
}

// 获取注册地址
func (s *server) GetRegAddrs() []string {
	regAddrs := make([]string, 0)
	for _, server := range s.servers {
		host := server.RegisterAddress()
		regAddrs = append(regAddrs, host)
	}
	return regAddrs
}
