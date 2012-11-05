/*
Copyright (c) 2012 Matthias S. Benkmann

This program is free software; you can redistribute it and/or
modify it under the terms of the GNU General Public License
as published by the Free Software Foundation; either version 2
of the License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
GNU General Public License for more details.

You should have received a copy of the GNU General Public License
along with this program; if not, write to the Free Software
Foundation, Inc., 51 Franklin Street, Fifth Floor, Boston, 
MA  02110-1301, USA.
*/

package message

import (
         "fmt"
         "net"
         "sync"
         "time"
         "sync/atomic"
         
         "../db"
         "../xml"
         "../util"
         "../util/deque"
         "../config"
       )

// A permanent connection to a peer. A PeerConnection is obtained via the
// Peer() function. All communication with peers that go-susi initiates is
// performed via PeerConnections.
// There are 2 modes of communication:
//  1) The asynchronous Ask() function. This opens a new connection to the peer
//     and runs in a new goroutine that waits for a reply.
//  2) The synchronous Tell() function. This sends all messages over the
//     PeerConnection.queue channel and from there a single goroutine passes them
//     on over a permanent TCP connection (which on the other side is serviced by
//     a single goroutine/thread). Replies are not permitted in this case (because
//     proper synchronization is hard to achieve when some messages have and others
//     don't have replies).
//
// The primary use for the synchronous channel is the sending of 
// foreign_job_updates, to make sure they all arrive in a well-defined order.
// See documentation in jobdb.go at handleJobDBRequests() for more information.
type PeerConnection struct {
  // true when the peer is known to speak the go-susi protocol.
  is_gosusi bool
  // FIFO for encrypted string-messages to be sent to the peer. 
  // Each string (typically foreign_job_updates) is sent to the peer
  // over the permanent TCP connection. The Tell() function enqueues messages here.
  queue deque.Deque
  // IP:PORT of the peer.
  addr string
  // nil for a normal PeerConnection. Non-nil for PeerConnection that is
  // created in non-working state and can only return this error on Ask().
  err error
  // The persistent TCP connection.
  tcpConn net.Conn
  // Unix time (seconds since the epoch) of the time the peer went down. 0 if it's up.
  // Needs to be accessed atomically because there is no locking on PeerConnection.
  whendown int64
}

// Tells this connection if its peer 
// advertises <loaded_modules>goSusi</loaded_modules>.
func (conn *PeerConnection) SetGoSusi(is_gosusi bool) {
  if is_gosusi {
    util.Log(1, "INFO! Peer %v uses go-susi protocol", conn.addr)
  } else {
    util.Log(1, "INFO! Peer %v uses old gosa-si protocol", conn.addr)
  }
  conn.is_gosusi = is_gosusi
}

// Returns the last value set via SetGoSusi() or false if SetGoSusi()
// has never been called.
func (conn *PeerConnection) IsGoSusi() bool {
  return conn.is_gosusi
}

// Returns how long this peer has been down (0 if everything is okay).
// After 7 days of downtime, the PeerConnection will first tell the jobdb to
// remove all jobs whose <siserver> is the broken peer, then the PeerConnection
// will dismantle itself.
func (conn *PeerConnection) Downtime() time.Duration {
  down := atomic.LoadInt64(&(conn.whendown))
  if down == 0 { return 0 }
  return (time.Duration(time.Now().Unix() - down)) * time.Second
}

// Encrypts msg with key and sends it to the peer without waiting for a reply.
// If key == "" the first key from db.ServerKeys(peer) is used.
func (conn *PeerConnection) Tell(msg, key string) {
  if conn.err != nil { return }
  if key == "" {
   keys := db.ServerKeys(conn.addr)
   if len(keys) == 0 {
     util.Log(0, "ERROR! PeerConnection.Tell: No key known for peer %v", conn.addr)
     return
   }
   key = keys[0]
  }
  util.Log(2, "DEBUG! Telling %v: %v", conn.addr, msg)
  encrypted := GosaEncrypt(msg, key)
  conn.queue.Push(encrypted)
}

// Encrypts request with key, sends it to the peer and returns a channel 
// from which the peer's reply can be received (already decrypted with
// the same key). It is guaranteed that a reply will
// be available from this channel even if the peer connection breaks
// or the peer does not reply within a certain time. In the case of
// an error, the reply will be an error reply (as returned by
// message.ErrorReply()). The returned channel will be buffered and
// the producer goroutine will close it after writing the reply. This
// means it is permissible to ignore reply without risk of a 
// goroutine leak.
func (conn *PeerConnection) Ask(request, key string) <-chan string {
  c := make(chan string, 1)
  
  if conn.err != nil {
    c<-ErrorReply(conn.err)
    close(c)
    return c
  }
  
  go util.WithPanicHandler(func(){
    defer close(c)
    tcpconn, err := net.Dial("tcp", conn.addr)
    if err != nil {
      c<-ErrorReply(err)
    } else {
      defer tcpconn.Close()
      util.Log(2, "DEBUG! Asking %v: %v", conn.addr, request)
      util.SendLn(tcpconn, GosaEncrypt(request, key), config.Timeout)
      reply := GosaDecrypt(util.ReadLn(tcpconn, config.Timeout), key)
      if reply == "" { reply = "General communication error" } 
      util.Log(2, "DEBUG! Reply from %v: %v", conn.addr, reply)
      c<-reply
    }
  })
  return c
}

// Calls SyncAll() after a few seconds delay if this connection's peer is not
// a go-susi. This is used after foreign_job_updates has been sent, because
// gosa-si (unlike go-susi) does not broadcast changes it has done in reaction
// to foreign_job_updates.
func (conn* PeerConnection) SyncNonGoSusi() {
  if conn.IsGoSusi() { return }
  go func() {
    time.Sleep(5*time.Second) // 5s should be enough, even for gosa-si
    conn.SyncAll()
  }()
}

// Sends all local jobs to the peer. If the peer is not a go-susi, also
// requests all of the peer's local jobs and converts them to a <sync>all</sync>
// message and feeds it into foreign_job_updates().
func (conn *PeerConnection) SyncAll() {
  if conn.IsGoSusi() {
    util.Log(1, "INFO! Full sync (go-susi protocol) with %v", conn.addr)
    db.JobsSyncAll(conn.addr, nil)
  } else 
  { // peer is not go-susi (or not known to be one, yet)
    go util.WithPanicHandler(func() {
      util.Log(1, "INFO! Full sync (gosa-si fallback) with %v", conn.addr)
      
      // Query the peer's database for 
      // * all jobs the peer is responsible for
      // * all jobs the peer thinks we are responsible for
      query := xml.NewHash("xml","header","gosa_query_jobdb")
      query.Add("source", "GOSA")
      query.Add("target", "GOSA")
      clause := query.Add("where").Add("clause")
      clause.Add("connector", "or")
      clause.Add("phrase").Add("siserver","localhost")
      clause.Add("phrase").Add("siserver",conn.addr)
      clause.Add("phrase").Add("siserver",config.ServerSourceAddress)
      
      jobs_str := <- conn.Ask(query.String(), config.ModuleKey["[GOsaPackages]"])
      jobs, err := xml.StringToHash(jobs_str)
      if err != nil {
        util.Log(0, "ERROR! gosa_query_jobdb: Error decoding reply from peer %v: %v", conn.addr, err)
        // Bail out. Otherwise we would end up removing all of the peer's jobs from
        // our database if the peer is down. While that would be one way of dealing
        // with this case, we prefer to keep those jobs and convert them into
        // state "error" with an error message about the downtime. This happens
        // in gosa_query_jobdb.go.
        return 
      }
      
      if jobs.First("error_string") != nil { 
        util.Log(0, "ERROR! gosa_query_jobdb: Peer %v returned error: %v", conn.addr, jobs.Text("error_string"))
        // Bail out. See explanation further above.
        return 
      }
      
      // Now we extract from jobs those that are the responsibility of the
      // peer and synthesize a foreign_job_updates with <sync>all</sync> from them.
      // This leaves in jobs those the peer believes belong to us.
      
      fju := jobs.Remove(xml.FilterOr([]xml.HashFilter{xml.FilterSimple("siserver","localhost"),xml.FilterSimple("siserver",conn.addr)}))
      fju.Rename("xml")
      fju.Add("header","foreign_job_updates")
      fju.Add("source", conn.addr)
      fju.Add("target", config.ServerSourceAddress)
      fju.Add("sync", "all")
      
      util.Log(2, "DEBUG! Queuing synthetic fju: %v", fju)
      foreign_job_updates(fju)
      
      db.JobsSyncAll(conn.addr, jobs)
    })
  }
}

// Must be called in a separate goroutine for each newly created PeerConnection.
// Will not return without first removing all jobs associated with the peer from
// jobdb and removing the PeerConnection itself from the connections list.
func (conn *PeerConnection) handleConnection() {
  var err error
  conn.tcpConn, err = net.Dial("tcp", conn.addr)
  if err != nil {
    util.Log(0, "ERROR! PeerConnection: %v",err)
    return
  }
  err = conn.tcpConn.(*net.TCPConn).SetKeepAlive(true)
  if err != nil {
    util.Log(0, "ERROR! SetKeepAlive: %v", err)
  }
          
  for {
    message := conn.queue.Next().(string)
    util.SendLn(conn.tcpConn, message, config.Timeout)
  }
  
/*
After 7 days of downtime, the PeerConnection gives up, issues a JobsRemoveForeign
for all jobs belonging to the peer and removes itself (LOCK!!) from the connections
list, then the goroutine terminates.


gosa_query_jobdb uses JobsQuery to query for the respective jobs, then it
postprocesses the query and for each siserver that is down (cache the Downtime()
to get consistent results for all jobs) replace the status with error and inserts
a result with the message "SERVERNAME(from reverse lookup of ip) has been down for DURATION"


gosa_delete_jobdb_entry can not delete jobs from servers that are down. This is
a) good because it prevents overzealous admins from removing errors that other
   admins haven't seen yet.
b) an automatic result from the fact that foreign jobs are never removed directly
   but converted to fju+full sync (which fails if the server is down, leaving the
   old jobs intact)
*/

/*
  
  wenn eine abgebrochene Verbindung re-established wird 
  wird aufgerufen SyncAll()
  
  */

  /* 
  rogue Daten der Tell-Verbindung müssen ausgelesen (und geloggt) werden
  zur Sicherheit. Am einfachsten indem jeweils eine neue goroutine gestartet wird,
  die sich in Read() blockt und falls was kommt bzw. ein Fehler festgestellt wird
  auf einem chan error einen error sendet. Die Hauptgoroutine der PeerConnection
  blockt in select{}
  
  */
  
}

// Maps IP:ADDR to a PeerConnection object that talks to that peer. All accesses
// to connections are protected by connections_mutex.
var connections = map[string]*PeerConnection{}

// All access to connections must be protected by this mutex.
var connections_mutex sync.Mutex

// Returns a PeerConnection for talking to addr, which can be either
// IP:ADDR or HOST:ADDR (where HOST is something that DNS can resolve).
func Peer(addr string) *PeerConnection {
  host, port, err := net.SplitHostPort(addr)
  if err != nil {
    return &PeerConnection{err:err}
  }
  
  addrs, err := net.LookupIP(host)
  if err != nil {
    return &PeerConnection{err:err}
  }
  
  if len(addrs) == 0 {
    return &PeerConnection{err:fmt.Errorf("No IP address for %v",host)}
  }
  
  addr = addrs[0].String() + ":" + port
  
  connections_mutex.Lock()
  defer connections_mutex.Unlock()
  
  conn, have_already := connections[addr]
  if !have_already {
    conn = &PeerConnection{is_gosusi:false, addr:addr}
    connections[addr] = conn
    go util.WithPanicHandler(func(){conn.handleConnection()})
  }
  return conn
}

// Infinite loop to forward db.ForeignJobUpdates (see jobdb.go) 
// to the respective targets.
func init() {
  go func() {
    for fju := range db.ForeignJobUpdates {
      target := fju.Text("target")
      
      // see explanation in jobdb.go for var ForeignJobUpdates
      syncNonGoSusi := fju.RemoveFirst("SyncNonGoSusi")
      if syncNonGoSusi != nil && target != "" && !Peer(target).IsGoSusi() { 
        target = ""
      }
      
      if target != "" {
        Peer(target).Tell(fju.String(), "")
        if syncNonGoSusi != nil {
          Peer(target).SyncNonGoSusi()
        }
      } else
      { // send to ALL peers
        connections_mutex.Lock()
        for addr, peer := range connections {
          fju.First("target").SetText(addr)
          peer.Tell(fju.String(), "")
          if syncNonGoSusi != nil {
            peer.SyncNonGoSusi()
          }
        }
        connections_mutex.Unlock()
      }
    }
  }()
}



