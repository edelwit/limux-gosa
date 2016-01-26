/*
Copyright (c) 2015 Landeshauptstadt München
Author: Matthias S. Benkmann

This program is free software; you can redistribute it and/or
modify it under the terms of the GNU General Public License
as published by the Free Software Foundation; either version 2
of the License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.
*/

// Unit tests run by run-tests.go.
package tests

import (
         "os"
         "fmt"
         "net"
         "time"
         "crypto/tls"

         "../security"
         "../config"
         
         "github.com/mbenkmann/golib/util"
       )


// Unit tests for the package security.
func Security_test() {
  fmt.Printf("\n==== security ===\n\n")

  config.CACertPath = []string{"testdata/certs/ca.cert"}
  
  // do not spam console with expected errors but do
  // store them in the log file (if any is configured)
  util.LoggerRemove(os.Stderr)
  defer util.LoggerAdd(os.Stderr)

  cli, srv := tlsTest("1", "1")
  check(cli!=nil, true)
  check(srv!=nil, true)
  
  cli, srv = tlsTest("1", "2")
  check(cli!=nil, true)
  check(srv!=nil, true)

  cli, srv = tlsTest("nocert", "1")
  check(cli, nil)
  check(srv, nil)
  
  cli, srv = tlsTest("signedbywrongca", "1")
  check(cli!=nil, true)
  check(srv, nil)
  
  cli, srv = tlsTest("1","signedbywrongca")
  check(cli, nil)
  check(srv!=nil, true)
  
  cli, srv = tlsTest("local", "2")
  check(cli!=nil, true)
  check(srv!=nil, true)
  
  cli, srv = tlsTest("badip", "2")
  check(cli!=nil, true)
  check(srv, nil)
  
  cli, srv = tlsTest("badname", "2")
  check(cli!=nil, true)
  check(srv, nil)
  
  cli, srv = tlsTest("localname", "2")
  check(cli!=nil, true)
  check(srv, nil) // because *ocalhost does not match the actual resolved name
  
  security.SetMyServer("8.8.8.8")
  cli, srv = tlsTest("myserver", "2")
  check(cli!=nil, true)
  check(srv, nil)
  
  security.SetMyServer("127.0.0.1")
  cli, srv = tlsTest("myserver", "2")
  check(cli!=nil, true)
  check(srv!=nil, true)
  
  cli, srv = tlsTest("limits", "2")
  check(cli!=nil, true)
  if check(srv!=nil, true) {
    check(srv.Limits.TotalTime, time.Duration(98765)*time.Millisecond)
    check(srv.Limits.TotalBytes, 123456789012345)
    check(srv.Limits.MessageBytes, 76767542)
    check(srv.Limits.ConnPerHour, 3289)
    check(srv.Limits.ConnParallel, 348201284)
    check(srv.Limits.MaxLogFiles, 700499)
    check(srv.Limits.MaxAnswers, 4)
    check(srv.Limits.CommunicateWith, []string{"foo.tvc.muenchen.de:8089", "nobody", "1.2.3.4", "*"})
    check(srv.Access.Misc.Debug, true)
    check(srv.Access.Misc.Wake, false)
    check(srv.Access.Misc.Peer, true)
    check(srv.Access.Query.QueryAll, false)
    check(srv.Access.Query.QueryJobs, true)
    check(srv.Access.Jobs.JobsAll, false)
    check(srv.Access.Jobs.Lock, true)
    check(srv.Access.Jobs.Unlock, true)
    check(srv.Access.Jobs.Shutdown, false)
    check(srv.Access.Jobs.Wake, false)
    check(srv.Access.Jobs.Abort, true)
    check(srv.Access.Jobs.Install, true)
    check(srv.Access.Jobs.Update, true)
    check(srv.Access.Jobs.ModifyJobs, false)
    check(srv.Access.Jobs.NewSys, false)
    check(srv.Access.Incoming, []string{"ldap://*","ldaps://","foobar://foo.bar:987/blafasel"})
    check(srv.Access.LDAPUpdate.CN, false)
    check(srv.Access.LDAPUpdate.IP, true)
    check(srv.Access.LDAPUpdate.MAC, true)
    check(srv.Access.LDAPUpdate.DH, false)
    check(srv.Access.DetectedHW.Unprompted, true)
    check(srv.Access.DetectedHW.Template, false)
    check(srv.Access.DetectedHW.DN, true)
    check(srv.Access.DetectedHW.CN, true)
    check(srv.Access.DetectedHW.IPHostNumber, true)
    check(srv.Access.DetectedHW.MACAddress, false)
  }
  
  cli, srv = tlsTest("limits2", "2")
  check(cli!=nil, true)
  if check(srv!=nil, true) {
    check(srv.Limits.TotalTime, time.Duration(98765)*time.Millisecond)
    check(srv.Limits.TotalBytes, 123456789012345)
    check(srv.Limits.MessageBytes, 76767542)
    check(srv.Limits.ConnPerHour, 3289)
    check(srv.Limits.ConnParallel, 348201284)
    check(srv.Limits.MaxLogFiles, 700499)
    check(srv.Limits.MaxAnswers, 4)
    check(srv.Limits.CommunicateWith, []string{"foo.tvc.muenchen.de:8089", "nobody", "1.2.3.4", "*"})
    check(srv.Access.Misc.Debug, false)
    check(srv.Access.Misc.Wake, true)
    check(srv.Access.Misc.Peer, false)
    check(srv.Access.Query.QueryAll, true)
    check(srv.Access.Query.QueryJobs, false)
    check(srv.Access.Jobs.JobsAll, true)
    check(srv.Access.Jobs.Lock, false)
    check(srv.Access.Jobs.Unlock, false)
    check(srv.Access.Jobs.Shutdown, true)
    check(srv.Access.Jobs.Wake, true)
    check(srv.Access.Jobs.Abort, false)
    check(srv.Access.Jobs.Install, false)
    check(srv.Access.Jobs.Update, false)
    check(srv.Access.Jobs.ModifyJobs, true)
    check(srv.Access.Jobs.NewSys, true)
    check(srv.Access.Incoming, []string{"ldap://*","blafasel://foo.bar:987/foobar"})
    check(srv.Access.LDAPUpdate.CN, true)
    check(srv.Access.LDAPUpdate.IP, false)
    check(srv.Access.LDAPUpdate.MAC, false)
    check(srv.Access.LDAPUpdate.DH, true)
    check(srv.Access.DetectedHW.Unprompted, false)
    check(srv.Access.DetectedHW.Template, true)
    check(srv.Access.DetectedHW.DN, false)
    check(srv.Access.DetectedHW.CN, false)
    check(srv.Access.DetectedHW.IPHostNumber, false)
    check(srv.Access.DetectedHW.MACAddress, true)
  }
}

func tlsTest(client, server string) (*security.Context, *security.Context) {
  config.CertPath = "testdata/certs/" + server + ".cert"
  config.CertKeyPath = "testdata/certs/" + server + ".key"
  config.TLSServerConfig = nil
  config.TLSClientConfig = nil
  config.ReadCertificates()
  server_conf := config.TLSServerConfig
  
  client_conf := config.TLSClientConfig
  if client == "nocert" {
    client_conf.Certificates = nil
  } else { 
    config.CertPath = "testdata/certs/" + client + ".cert"
    config.CertKeyPath = "testdata/certs/" + client + ".key"
    config.TLSServerConfig = nil
    config.TLSClientConfig = nil
    config.ReadCertificates()
    client_conf = config.TLSClientConfig
  }
  
  if server_conf == nil || client_conf == nil {
    panic("TLS config broken")
  }
  
  c1 := make(chan *security.Context)
  c2 := make(chan *security.Context)
  
  go func() {
    tcp_addr, err := net.ResolveTCPAddr("tcp4", "127.0.0.1:18746")
    if err != nil { panic(err) }
    listener, err := net.ListenTCP("tcp4", tcp_addr)
    if err != nil { panic(err) }
    defer listener.Close()
    tcpConn, err := listener.AcceptTCP()
    if err != nil { panic(err) }
    defer tcpConn.Close()
    buf := []byte{'S','T','A','R','T','T','L','S','\n'}
    tcpConn.Read(buf)
    conn := tls.Server(tcpConn, server_conf)
    c2 <- security.ContextFor(conn)
  }()
  
  go func() {
    time.Sleep(1*time.Second)
    tcpConn, err := net.Dial("tcp4", "127.0.0.1:18746")
    if err != nil { panic(err) }
    defer tcpConn.Close()
    buf := []byte{'S','T','A','R','T','T','L','S','\n'}
    tcpConn.Write(buf)
    conn := tls.Client(tcpConn, client_conf)
    c1 <- security.ContextFor(conn)
  }()
  
  
  return  <-c1, <-c2
}
