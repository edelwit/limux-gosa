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

package tests

import (
         "io"
         "fmt"
         "net"
         "sync"
         "time"
         "bytes"
         "regexp"
         "strings"
         "io/ioutil"
         "container/list"
         "encoding/base64"
         
         "../db"
         "../xml"
         "../config"
         "../security"
         "github.com/mbenkmann/golib/util"
         "github.com/mbenkmann/golib/deque"
       )

// Regexp for recognizing valid MAC addresses.
var macAddressRegexp = regexp.MustCompile("^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$")
// Regexp for recognizing valid <client> elements of e.g. new_server messages.
var clientRegexp = regexp.MustCompile("^[0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}:[0-9]+,[:xdigit:](:[:xdigit:]){5}$")
// Regexp for recognizing valid <siserver> elements
var serverRegexp = regexp.MustCompile("^[0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}[.][0-9]{1,3}:[0-9]+$")

// The paths of the hook scripts. Filled in (and initially generated) by
// createConfigFile()
var generate_kernel_list = ""
var generate_package_list = ""

type Job struct {
  Type string
  MAC string
  Plainname string
  Timestamp string
  Periodic string
}

// returns Type with the "job_" removed.
func (self *Job) Trigger() string {
  return self.Type[4:]
}

var Jobs = []Job{
{"job_trigger_action_wake","01:02:03:04:05:06","systest1","20990914131742","7_days"},
{"job_trigger_action_lock","11:22:33:44:55:6F","systest2","20770101000000","1_minutes"},
{"job_trigger_action_wake","77:66:55:44:33:2a","systest3","20660906164734","none"},
{"job_trigger_action_localboot","0f:C3:d2:Aa:11:22","www","20000209024017","none"},
}

// Returns an XML hash for the job. Optional args can be the following:
//   int/uint: the name of the enclosing element will be answerX where X is the int
//             and the <id> will be X, too.
//   IP:PORT(string) : siserver  (default is listen_address)
func (job *Job) Hash(args... interface{}) *xml.Hash {
  x := xml.NewHash("answer1")
  x.Add("plainname", job.Plainname)
  x.Add("progress", "none")
  x.Add("status", "waiting")
  x.Add("siserver", listen_address)
  x.Add("modified", "1")
  x.Add("targettag", job.MAC)
  x.Add("macaddress", job.MAC)
  x.Add("timestamp", job.Timestamp)
  x.Add("periodic", job.Periodic)
  x.Add("id", "1")
  x.Add("headertag", job.Trigger())
  x.Add("result", "none")
  
  for _, arg := range args {
    switch arg := arg.(type) {
      case int:  x.Rename(fmt.Sprintf("answer%d",arg))
                 x.First("id").SetText("%d",arg)
      case uint: x.Rename(fmt.Sprintf("answer%d",arg))
                 x.First("id").SetText("%d",arg)
      case string:
                 if serverRegexp.MatchString(arg) {
                   x.First("siserver").SetText(arg)
                 } else {
                   panic("Unknown string format in Job.Hash()")
                 }
      default: panic("Unknown type in Job.Hash()")
    }
  }
  
  xm := xml.NewHash("xml","header", "job_" + x.Text("headertag"))
  xm.Add("source", "GOSA")
  xm.Add("target", x.Text("targettag"))
  xm.Add("timestamp", x.Text("timestamp"))
  xm.Add("macaddress", x.Text("macaddress"))
  x.Add("xmlmessage", base64.StdEncoding.EncodeToString([]byte(xm.String())))
  return x
}

type queueElement struct {
  // the decoded message. If an error occurred during decoding, this will be
  // <error>Message</error>.
  XML *xml.Hash
  // The time at which the message was received.
  Time time.Time
  // The key with which the message was encrypted.
  Key string
  // IP address of sender.
  SenderIP string 
  // true if the message was received via client_listener instead of listener
  IsClientMessage bool
}

// All incoming messages are appended to the queue. Access protected by queue_mutex
var queue = []*queueElement{}
// queue must only be accessed while holding queue_mutex.
var queue_mutex sync.Mutex

// max time to wait for a reply
var reply_timeout = 2000 * time.Millisecond

// port the test server listens on for new_server etc.
var listen_port = "18340" 

// host:port address of the test server.
var listen_address string

// the listener of the test server
var listener net.Listener

// port the test client listens on
var client_listen_port = "18341" 

// host:port address of the test client.
var client_listen_address string

// the listener of the test client
var client_listener net.Listener


// Elements of type net.Conn for all current active incoming connections
// handled by handleConnection()
var active_connections = deque.New()

// keys[0] is the key of the test server started by listen(). 
// keys[len(keys)-1] is the key of the test client started by listen()
// The other elements are copies of config.ModuleKeys
// ATTENTION! You must call init_keys() to initialize this.
var keys []string

// returns all messages currently in the queue that were received at time t or later.
func get(t time.Time) []*queueElement {
  queue_mutex.Lock()
  defer queue_mutex.Unlock()
  
  result := []*queueElement{}
  for _, q := range queue {
    if !q.Time.Before(t) {
      result = append(result, q)
    }
  }
  
  return result
}

// Waits at most until time t+reply_timeout for a message that is/was received
// at time t or later with the given header and returns that message. If none
// is received within the timeframe, a dummy message is returned.
func wait(t time.Time, header string) *queueElement {
  end_time := t.Add(reply_timeout)
  for {
    queue_mutex.Lock()
    for _, q := range queue {
      if !q.Time.Before(t) && q.XML.Text("header") == header {
        queue_mutex.Unlock()
        return q
      }
    }
    queue_mutex.Unlock()
    if time.Now().After(end_time) { break }
    time.Sleep(100*time.Millisecond)
  }
  
  return &queueElement{xml.NewHash("xml"), time.Now(), "", "0.0.0.0", false}
}

// like wait but waits some additional seconds
// This used to be necessary for waiting for client messages because go-susi
// intentionally put delays between them to ensure they are processed in the
// proper order. This behaviour has been removed again because ATM there
// does not seem to be a reason to enforce order. However I keep waitlong()
// around because the wait may come back.
func waitlong(t time.Time, header string) *queueElement {
  old_reply_timeout := reply_timeout
  reply_timeout += 3*time.Second
  defer func() { reply_timeout = old_reply_timeout }()
  return wait(t,header)
}

// sends the xml message x to the gosa-si/go-susi server being tested
// (config.ServerSourceAddress) encrypted with the module key identified by keyid
// (e.g. "[ServerPackages]"). Use keyid "" to select the server key exchanged via
// new_server/confirm_new_server
// Use keyid "CLIENT" to select keys[len(keys)-1]
// If x does not have <target> and/or <source> elements, they will be added
// with the values config.ServerSourceAddress and listen_address respectively.
//
// ATTENTION! This method does not wait for a reply from the server.
// Therefore you will usually need to wait a little for the server to have
// processed the message before checking for effects.
func send(keyid string, x *xml.Hash) {
  var key string
  if keyid == "" { key = keys[0] } else 
  if keyid == "CLIENT" { key = keys[len(keys)-1] } else
  { 
    key = config.ModuleKey[keyid] 
  }
  if x.First("source") == nil {
    x.Add("source", listen_address)
  }
  if x.First("target") == nil {
    x.Add("target", config.ServerSourceAddress)
  }
  util.SendLnTo(config.ServerSourceAddress, security.GosaEncrypt(x.String(), key), config.Timeout)
}

// Sends a GOSA message to the server being tested and
// returns the reply.
// Automatically adds <header>gosa_typ</header> (unless typ starts with "job_" 
// or "gosa_" in which case <header>typ</header> will be used.)
// and <source>GOSA</source> as well as <target>GOSA</target> (unless a subelement
// of the respective name is already present).
func gosa(typ string, x *xml.Hash) *xml.Hash {
  return gosa2(typ, x, true)
}

// Like gosa() but does not read a reply.
func gosa_noreply(typ string, x *xml.Hash) {
  gosa2(typ, x, false)
}

// Like gosa() but if read_reply == false, no reply will be read and nil
// will be returned
func gosa2(typ string, x *xml.Hash, read_reply bool) *xml.Hash {
  if !strings.HasPrefix(typ, "gosa_") && !strings.HasPrefix(typ, "job_") {
    typ = "gosa_" + typ
  }
  if x.First("header") == nil {
    x.Add("header", typ)
  }
  if x.First("source") == nil {
    x.Add("source", "GOSA")
  }
  if x.First("target") == nil {
    x.Add("target", "GOSA")
  }
  conn, err := net.Dial("tcp", config.ServerSourceAddress)
  if err != nil {
    util.Log(0, "ERROR! Dial: %v", err)
    return xml.NewHash("error")
  }
  defer conn.Close()
  util.SendLn(conn, security.GosaEncrypt(x.String(), config.ModuleKey["[GOsaPackages]"]), config.Timeout)
  if read_reply {
    reply,err := util.ReadLn(conn, config.Timeout)
    reply = security.GosaDecrypt(reply, config.ModuleKey["[GOsaPackages]"])
    if err == nil {
      x, err = xml.StringToHash(reply)
    }
    if err != nil { x = xml.NewHash("error") }
    if err != nil {
      util.Log(0, "ERROR! While reading reply in test-helpers.go:gosa(): %v\nIf this is a timeout error for a request that does not return a reply, use gosa_noreply() instead of gosa()", err)
      time.Sleep(60*time.Second)
    }
  }
  return x
}

// creates a temporary config file and returns the path to it as well as the
// path to the containing temporary directory.
// Also creates initial hook files for system-test.go:run_hook_tests()
func createConfigFile(prefix, addresses string) (conffile, confdir string) {
  tempdir, err := ioutil.TempDir("", prefix)
  if err != nil { panic(err) }
  
  generate_kernel_list = tempdir+"/generate_kernel_list" 
  ioutil.WriteFile(generate_kernel_list, []byte(`#!/bin/bash
echo
echo
echo "cn: tobi"
echo "release: ignaz"
echo
echo "cn: michael"
echo "release: ignaz"
echo
echo
echo "cn: jan-marek"
echo "release: dennis"
`), 0755)

  generate_package_list = tempdir+"/generate_package_list"
  ioutil.WriteFile(generate_package_list, []byte(`#!/bin/bash
echo "
Release: kuschel
Package: baer

Package: faultier
Release: kuschel
Description: knuddelig und langsam
Version: 9.8
Section: tree
Templates: foo

Package: otter
Description: schwimmtier
Version: notused
Release: pluesch
Release: kuschel
Release: flausch
Version: 3.4
Version: 3.5
Section: wasser
Templates: spezi
Release: doux
Release: weich
Release: soft
Version: 3.6
Section: fluss
Template: paulaner
Description: wassertier
"
`), 0755)

  generate_pxelinux_cfg := tempdir+"/generate_pxelinux_cfg"
  ioutil.WriteFile(generate_pxelinux_cfg, []byte(`#!/bin/bash
echo $macaddress
echo $tftp_request
`), 0755)

  foo_sh := tempdir+"/foo.sh"
  ioutil.WriteFile(foo_sh, []byte(`#!/bin/bash
echo $macaddress
echo $mac
echo $#
echo $1$cn
echo $2
echo $tftp_request
`), 0755)

  send_user_msg := tempdir+"/send_user_msg"
  ioutil.WriteFile(send_user_msg, []byte(`#!/bin/bash
set >"$0.env"
`), 0755)

  update_config := tempdir+"/update_config"
  ioutil.WriteFile(update_config, []byte(`#!/bin/bash
exec >>`+tempdir+`/config.txt

test -n "$new_ldap_config" && {
  echo $department
  echo $admin_base
  for l in $ldap_uri ; do echo $l ; done
}

test -n "$new_ntp_config" && {
  for s in $server ; do echo $s ; done
}
exit 0
`), 0755)

  trigger_action := tempdir+"/trigger_action"
  ioutil.WriteFile(trigger_action, []byte(`#!/bin/bash
case $header in
  trigger_action_audit)
                        echo TASKEND audit >>`+tempdir+`/fai-monitor.log
                        ;;
esac
exit 0
`), 0755)

  fai_progress := tempdir+"/fai_progress"
  ioutil.WriteFile(fai_progress, []byte(`#!/bin/bash
touch `+tempdir+`/fai-monitor.log
exec tail -f `+tempdir+`/fai-monitor.log
`), 0755)

  fai_audit := tempdir+"/fai_audit"
  ioutil.WriteFile(fai_audit, []byte(`#!/bin/bash
echo -n log_file:foo.xml:
echo "<audit>
</audit>
" | base64 -w 0
echo
echo -n log_file:bar.xml:
echo "<audit>
<entry>
<key>feuerwehr</key>
<sirene>laut</sirene>
</entry>
<entry>
<key>bullizei</key>
<sirene>nervig</sirene>
</entry>
</audit>
" | base64 -w 0
echo
echo audit
read
exit 0
`), 0755)

  pxelinux := tempdir+"/pxelinux.txt"
  ioutil.WriteFile(pxelinux, []byte("This is\000pxelinux.0"), 0644)
  
  fpath := tempdir + "/server.conf"
  ioutil.WriteFile(fpath, []byte(`
[general]
log-file = `+tempdir+`/go-susi.log
pid-file = `+tempdir+`/go-susi.pid
kernel-list-hook = `+tempdir+`/generate_kernel_list
package-list-hook = `+tempdir+`/generate_package_list
user-msg-hook = `+tempdir+`/send_user_msg
pxelinux-cfg-hook = `+tempdir+`/generate_pxelinux_cfg
new-config-hook = `+tempdir+`/update_config
trigger-action-hook = `+tempdir+`/trigger_action
fai-progress-hook = `+tempdir+`/fai_progress
fai-audit-hook = `+tempdir+`/fai_audit

[bus]
enabled = false
key = bus

[server]
port = 20087
max-clients = 10000
ldap-uri = ldap://127.0.0.1:20088
ldap-base = o=go-susi,c=de
ldap-admin-dn = cn=admin,o=go-susi,c=de
ldap-admin-password = password

[client]
port = 20997, 20998  20999

[tftp]
port = 20069
/pxelinux.0 = `+tempdir+`/pxelinux.txt
/^foo-(?P<mac>(?P<macaddress>.*)) = |`+tempdir+`/foo.sh fox hound
/^blarg =  
/false = |/bin/false

[faimon]
port = 24711

[ClientPackages]
key = ClientPackages

[ArpHandler]
enabled = false

[GOsaPackages]
enabled = true
key = GOsaPackages

[ldap]
bind_timelimit = 5

[pam_ldap]
bind_timelimit = 5

[nss_ldap]
bind_timelimit = 5

[ServerPackages]
key = ServerPackages
dns-lookup = false
address = `+addresses+`

`), 0644)

  extra_servers_ous := tempdir+"/ou=servers.conf" 
  ioutil.WriteFile(extra_servers_ous, []byte(`
ou=servers,ou=systems,o=go-strolch,c=de
`), 0644)

  return fpath, tempdir
}

// Takes a format string like "xml(foo(%v)bar(%v))" and parameters and creates
// a corresponding xml.Hash.
func hash(format string, args... interface{}) *xml.Hash {
  format = fmt.Sprintf(format, args...)
  stack := list.New()
  output := []string{}
  a := 0
  for b := range format {
    switch format[b] {
      case '(' : tag := format[a:b]
                 stack.PushBack(tag)
                 if tag != "" {
                   output = append(output, "<" + tag + ">")
                 }
                 a = b + 1
      case ')' : output = append(output, format[a:b])
                 a = b + 1
                 tag := stack.Back().Value.(string)
                 stack.Remove(stack.Back())
                 if tag != "" {
                   output = append(output, "</" + tag + ">")
                 }
    }
  }
  
  hash, err := xml.StringToHash(strings.Join(output, ""))
  if err != nil { panic(err) }
  return hash
}

// Returns a hash of the form
// <xml>
//   <answer>...</answer>
//   <answer>...</answer>
//   ...
// </xml>
//
// where each <answer> element corresponds to an <answerX> element from
// the input x. The answers are sorted by the String() values.
func extract_sorted_answers(x *xml.Hash) *xml.Hash {
  result := xml.NewHash("xml")

  answers := []*xml.Hash{}
  
  for _, tag := range x.Subtags() {
    if !strings.HasPrefix(tag, "answer") { continue }
    for ele := x.First(tag); ele != nil; ele = ele.Next() {
      answer := ele.Clone()
      answer.Rename("answer")
      answers = append(answers, answer)
    }
  }
  
  // sort
  for i := range answers {
    for k := i+1; k < len(answers); k++ {
      if answers[k].String() < answers[i].String() {
        answers[i],answers[k] = answers[k],answers[i]
      }
    }
  }
  
  for i := range answers {
    result.AddWithOwnership(answers[i])
  }
  
  return result
}

// Checks if x has the given tags and if there is a difference, returns a
// string describing the issue. If everything's okay, returns "".
//  taglist: A comma-separated string of tag names. A tag may be followed by "?"
//        if it is optional, "*" if 0 or more are allowed or "+" if 1 or more
//        are allowed.
//        x is considered okay if it has all non-optional tags from the list and
//        has no unlisted tags and no tags appear more times than permitted. 
func checkTags(x *xml.Hash, taglist string) string {
  if x == nil { return "No data" }
  tags := map[string]bool{}
  for _, tag := range strings.Split(taglist, ",") {
    switch tag[len(tag)-1] {
      case '?': tag := tag[0:len(tag)-1]
                tags[tag] = true
                if len(x.Get(tag)) > 1 {
                  return(fmt.Sprintf("More than 1 <%v>", tag))
                }
      case '*': tag := tag[0:len(tag)-1]
                tags[tag] = true
      case '+': tag := tag[0:len(tag)-1]
                tags[tag] = true
                if len(x.Get(tag)) == 0 {
                  return(fmt.Sprintf("Missing <%v>", tag))
                }
      default: 
                if len(x.Get(tag)) == 0 {
                  return(fmt.Sprintf("Missing <%v>", tag))
                }
                if len(x.Get(tag)) > 1 {
                  return(fmt.Sprintf("More than 1 <%v>", tag))
                }
                tags[tag] = true
    }
  }
  
  for _, tag := range x.Subtags() {
    if !tags[tag] {
      return(fmt.Sprintf("Unknown <%v>", tag))
    }
  }
  
  return ""
}



// Waits until all pending changes to jobdb are processed, then returns all
// messages from db.ForeignJobUpdates.
func getFJU() []*xml.Hash {
  db.JobsQuery(xml.FilterNone) // make sure previous calls have been processed
  ret := []*xml.Hash{}
  for {
    select {
      case f := <- db.ForeignJobUpdates: ret = append(ret, f)
      default: return ret
    }
  }
  return ret
}

//initializes var keys. ATTENTION! Must be called after config.* is initialized
func init_keys() {
  keys = make([]string, len(config.ModuleKeys)+2)
  for i := range config.ModuleKeys { keys[i+1] = config.ModuleKeys[i] }
  keys[0] = "none"
  keys[len(keys)-1] = "client_key"
}  

// Returns "" if all words are contained in text, otherwise returns an error message.
func hasWords(text interface{}, words... string) string {
  txt := fmt.Sprintf("%v",text)
  missing := ""
  for _, w := range words {
    if strings.Index(txt, w) < 0 { if missing != "" { missing += ", " }; missing += "\""+w+"\"" }
  }
  if missing != "" { return "Missing word(s) " + missing + " in \"" + txt +"\"" }
  return ""
}

// sets up 2 listening ports (one for client and one for server) that receive 
// messages, decrypt them and store them in the queue.
func listen() {
  var err error
  listener, err = net.Listen("tcp", ":" + listen_port)
  if err != nil { panic(fmt.Sprintf("Test cannot run. Fatal error: %v", err)) }
  
  client_listener, err = net.Listen("tcp", ":" + client_listen_port)
  if err != nil { panic(fmt.Sprintf("Test cannot run. Fatal error: %v", err)) }
  
  go func() {
    defer listener.Close()
    
    for {
      conn, err := listener.Accept()
      if err != nil { return }
      
      go handleConnection(conn, false)
    }
  }()
  
  go func() {
    defer client_listener.Close()
    
    for {
      conn, err := client_listener.Accept()
      if err != nil { return }
      
      go handleConnection(conn, true)
    }
  }()
}

// shuts down the listener and all currently active connections
func listen_stop() {
  listener.Close()
  client_listener.Close()
  for {
    connection := active_connections.PopAt(0)
    if connection == nil { break }
    connection.(net.Conn).Close()
  }  
}

// handles an individual connection received by listen().
func handleConnection(conn net.Conn, is_client bool) {
  defer conn.Close()
  active_connections.Push(conn)
  defer active_connections.Remove(conn)
  
  senderIP,_,_ := net.SplitHostPort(conn.RemoteAddr().String())
  // translate loopback address to our own external IP  
  if senderIP == "127.0.0.1" { senderIP = config.IP }
  
  conn.(*net.TCPConn).SetKeepAlive(true)
  
  var err error
  
  var buf = make([]byte, 65536)
  i := 0
  n := 1
  for n != 0 {
    n, err = conn.Read(buf[i:])
    i += n
    
    if err != nil && err != io.EOF {
      break
    }
    if err == io.EOF {
      err = nil
      break
    }
    if n == 0 && err == nil {
      err = fmt.Errorf("Read 0 bytes but no error reported")
      break
    }
    
    if i == len(buf) {
      buf_new := make([]byte, len(buf)+65536)
      copy(buf_new, buf)
      buf = buf_new
    }

    // Find complete lines terminated by '\n' and process them.
    for start := 0;; {
      eol := bytes.IndexByte(buf[start:i], '\n')
      
      // no \n found, go back to reading from the connection
      // after purging the bytes processed so far
      if eol < 0 {
        copy(buf[0:], buf[start:i]) 
        i -= start
        break
      }
      
      // process the message and get a reply (if applicable)
      reply := processMessage(string(buf[start:start+eol]), senderIP, is_client)
      if reply != "" { util.SendLn(conn, reply, 5*time.Second) }
      start += eol+1
    }
  }
  
  if  i != 0 {
    err = fmt.Errorf("ERROR! Incomplete message (i.e. not terminated by \"\\n\") of %v bytes: %v", i, buf[0:i])
  }
  
  if err != nil {
    msg := queueElement{IsClientMessage:is_client}
    msg.XML = hash("error(%v)", err)
    msg.Time = time.Now()
    msg.SenderIP = senderIP
  
    queue_mutex.Lock()
    defer queue_mutex.Unlock()
    queue = append(queue, &msg)
  }
}

func processMessage(str string, senderIP string, is_client bool) string {
  str = strings.TrimSpace(str)
  if str == "" { return "" } // ignore empty messages
  
  var err error
  msg := queueElement{IsClientMessage:is_client}
  
  decrypted := ""
  for _, msg.Key = range keys {
    //fmt.Printf("Trying key %v\n",msg.Key)
    decrypted = security.GosaDecrypt(str, msg.Key)
    if decrypted != "" { break }
  }
  if decrypted == "" {
    err = fmt.Errorf("Could not decrypt message")
  } else {
    msg.XML, err = xml.StringToHash(decrypted)
  }

  if err != nil {
    msg.XML = hash("error(%v)", err)
  }

  // if we get a new_server or confirm_new_server message, update our server key  
  header := msg.XML.Text("header")
  if header == "new_server" || header == "confirm_new_server" {
    keys[0] = msg.XML.Text("key")
  }
  
  // The test server advertises "goSusi" in loaded_modules, so it is
  // required to confirm changes made to its jobs via foreign_job_updates
  if header == "foreign_job_updates" {
    for _, tag := range msg.XML.Subtags() {
      if !strings.HasPrefix(tag, "answer") { continue }
      for job := msg.XML.First(tag); job != nil; job = job.Next() {
        if job.Text("siserver") == listen_address {
          fju := xml.NewHash("xml","header","foreign_job_updates")
          fju.AddClone(job)
          send("", fju)
        }
      }
    }
  }
  
  
  msg.Time = time.Now()
  msg.SenderIP = senderIP
  //fmt.Printf("Received %v\n", msg.XML.String())
  
  queue_mutex.Lock()
  defer queue_mutex.Unlock()
  queue = append(queue, &msg)
  
  // Because initially go-susi doesn't know that we're also "goSusi"
  // it may ask as for our database, so we need to be able to respond
  if header == "gosa_query_jobdb" {
    emptydb := fmt.Sprintf("<xml><header>query_jobdb</header><source>%v</source><target>GOSA</target></xml>",listen_address)
    return security.GosaEncrypt(emptydb, config.ModuleKey["[GOsaPackages]"])
  }
  
  return ""
}

