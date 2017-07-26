/*
Copyright (c) 2013 Matthias S. Benkmann

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
         "math/rand"
         "time"
         "strings"
         "sync/atomic"
         
         "../db"
         "../xml"
         "github.com/mbenkmann/golib/util"
         "../config"
         "../security"
       )

// If non-nil, these are additional fields sent in here_i_am message.
// The root element name is irrelevant. Elements must not have children.
var Here_I_Am_Extra *xml.Hash

var TotalRegistrations int32
var MissedRegistrations int32

// sends a here_i_am message to target (HOST:PORT).
func Send_here_i_am(target string) {
  security.SetMyServer(target)
  here_i_am := xml.NewHash("xml", "header", "here_i_am")
  here_i_am.Add("here_i_am")
  here_i_am.Add("source", config.ServerSourceAddress)
  here_i_am.Add("target", target)
  here_i_am.Add("client_version", config.Version)
  here_i_am.Add("client_revision", config.Revision)
  here_i_am.Add("mac_address", config.MAC) //Yes, that's mac_address with "_"
  here_i_am.Add("broadcast", config.Broadcast)
  
  if Here_I_Am_Extra != nil {
    for info := Here_I_Am_Extra.FirstChild(); info != nil; info = info.Next() {
      here_i_am.Add(info.Element().Name(), info.Element().Text())
    }
  }
  
  clientpackageskey := config.ModuleKey["[ClientPackages]"]
  // If [ClientPackages]/key missing, take the last key in the list
  // (We don't take the 1st because that would be "dummy-key").
  if clientpackageskey == "" { clientpackageskey = config.ModuleKeys[len(config.ModuleKeys)-1] }
  
  // If we have a certificate, we only register at servers that support
  // TLS. The empty string key in the client database signals to the server
  // that it should use TLS when contacting us.
  if config.TLSClientConfig != nil {
    clientpackageskey = ""
  }
  
  // We don't generate random keys as it adds no security.
  // Everybody who has the ClientPackages key can decrypt the
  // key exchange messages, so a random key would only be as
  // secure as the ClientPackages key itself.
  here_i_am.Add("key_lifetime","2147483647")
  here_i_am.Add("new_passwd", clientpackageskey)
  
  util.Log(2, "DEBUG! Sending here_i_am to %v: %v", target, here_i_am)
  security.SendLnTo(target, here_i_am.String(), clientpackageskey, false)
}



// Handles the message "here_i_am".
//  xmlmsg: the decrypted and parsed message
func here_i_am(xmlmsg *xml.Hash) {
  start := time.Now()
  client_addr := xmlmsg.Text("source")
  macaddress  := xmlmsg.Text("mac_address") //Yes, that's "mac_address" with "_"
  if !macAddressRegexp.MatchString(macaddress) {
    util.Log(0, "WARNING! here_i_am from client with illegal MAC \"%v\" (IP: %v)", macaddress, client_addr)
    return
  }
  
  if client_addr != config.ServerSourceAddress { // do not throttle our internal client
    if strikes := db.ClientThrottle(macaddress); strikes > 10 { 
      util.Log(0, "WARNING! Throttling client %v (%v strikes)", client_addr, strikes)
      if rand.Intn(strikes) != 0 { // client has a 1 in strikes chance of getting through
        return
      }  
    }
  }
  
  util.Log(1, "INFO! here_i_am from client %v (%v)", client_addr, macaddress)
  
  client := xml.NewHash("xml","header","new_foreign_client")
  client.Add("new_foreign_client")
  client.Add("source",config.ServerSourceAddress)
  client.Add("target",config.ServerSourceAddress)
  client.Add("client", client_addr)
  client.Add("macaddress",macaddress)
  client.Add("key",xmlmsg.Text("new_passwd"))
  
  /*
    Copy additional info from hia into the clientdb entry (and nfc)
  */
  rest_size := config.HIAMaxInfoSize
  for info := xmlmsg.FirstChild(); info != nil; info = info.Next() {
    name := info.Element().Name()
    switch name {
      case "header", "source", "target", "here_i_am", "mac_address", "new_passwd",
           "key_lifetime": // special elements we don't copy
      default:
           value := info.Element().Text()
           if len(value)+len(name) > config.HIAMaxElementSize {
             util.Log(0, "WARNING! here_i_am from client %v (%v) contains overlong info element => Some info not stored in clientdb", client_addr, macaddress)
             goto copy_end
           }
           rest_size -= len(value)+len(name)
           if rest_size < 0 {
             util.Log(0, "WARNING! here_i_am from client %v (%v) contains too much info => Some info not stored in clientdb", client_addr, macaddress)
             goto copy_end
           }
           client.Add(name, value)
    }
  }
copy_end:
  
  db.ClientUpdate(client)
  // A client that sends here_i_am to us is either our own internal client or
  // a client-only client. In either case make sure we don't have an entry in the
  // peer database.
  db.ServerRemove(client_addr)
  
  util.Log(1, "INFO! Getting LDAP data for client %v (%v) including groups", client_addr, macaddress)
  system, err := db.SystemGetAllDataForMAC(macaddress, true)
  if _, not_found := err.(db.SystemNotFoundError); err != nil && !not_found {
    // If we encounter any error other than "system not found" we can't continue
    util.Log(0, "ERROR! LDAP error searching for %v: %v", macaddress, err)
    return
  }
  
  checkTime(start, macaddress)
  
  util.Log(1, "INFO! Informing all peers about new registered client %v at %v", macaddress, client_addr)
  for _, server := range db.ServerAddresses() {
    client.First("target").SetText(server)
    Peer(server).Tell(client.String(), "")
  }
  checkTime(start, macaddress)

  message_start := "<xml><source>"+config.ServerSourceAddress+"</source><target>"+client_addr+"</target>"
  registered := message_start + "<header>registered</header><registered></registered>"
  
  if system != nil && system.Text("gotoldapserver") != "" {
    registered += "<ldap_available>true</ldap_available>"
  }
  registered += "</xml>"
  Client(client_addr).Tell(registered, config.RegisteredMessageTTL)
  atomic.AddInt32(&TotalRegistrations, 1)
  if !checkTime(start, macaddress) { atomic.AddInt32(&MissedRegistrations, 1) }
  
  // gosa-si puts incoming messages into incomingdb and then
  // processes them in the order they are returned by the database
  // which causes messages to be processed in the wrong order.
  // If gosa-si-client processes a new_ldap_config message before
  // the registered message this may cause it to hang.
  // To counteract this we wait a little after sending "registered".
  time.Sleep(1000*time.Millisecond)
  
  if err != nil { // if no LDAP data available for system, create install job, do hardware detection
    if client_addr == config.ServerSourceAddress {
      util.Log(1, "INFO! %v => Normally I would create an install job and send detect_hardware, but the here_i_am is from myself, so I better not saw the branch I'm sitting on.", err)
    } else {
      util.Log(1, "INFO! %v => Creating install job and sending detect_hardware to %v", err, macaddress)
    
      detect_hardware := message_start + "<header>detect_hardware</header><detect_hardware></detect_hardware></xml>"
      Client(client_addr).Tell(detect_hardware, config.NormalClientMessageTTL)
    
      makeSureWeHaveAppropriateProcessingJob(macaddress, "trigger_action_reinstall", "hardware-detection")
    }

  } else { // if LDAP data for system is available
    
    Send_new_ldap_config(client_addr, system)
    
    util.Log(1, "INFO! Making sure jobs for %v are consistent with faistate \"%v\"", macaddress, system.Text("faistate"))
    
    switch (system.Text("faistate")+"12345")[0:5] {
      case "local":  local_processing := xml.FilterSimple("siserver", config.ServerSourceAddress, "macaddress", macaddress, "status", "processing")
                     install_or_update := xml.FilterOr([]xml.HashFilter{xml.FilterSimple("headertag", "trigger_action_reinstall"),xml.FilterSimple("headertag", "trigger_action_update")})
                     local_processing_install_or_update := xml.FilterAnd([]xml.HashFilter{local_processing, install_or_update})
                     db.JobsModifyLocal(local_processing_install_or_update, xml.NewHash("job","progress","groom")) // to prevent faistate => localboot
                     db.JobsRemoveLocal(local_processing_install_or_update, false) // false => re-schedule if periodic
      case "reins",
           "insta": makeSureWeHaveAppropriateProcessingJob(macaddress, "trigger_action_reinstall", "none")
      case "updat",
           "softu": makeSureWeHaveAppropriateProcessingJob(macaddress, "trigger_action_update", "none")
      case "error":
    }
    
    // Update LDAP entry if cn != DNS name  or ipHostNumber != IP
    client_addr := strings.SplitN(client_addr,":",2)
    client_ip := client_addr[0]
    client_port := ""
    if len(client_addr) > 1 { client_port = client_addr[1] }
    client_name := strings.ToLower(db.SystemNameForIPAddress(client_ip))
    new_name := strings.SplitN(client_name,".",2)[0]
    if config.FullQualifiedCN { new_name = client_name }
    uses_standard_port := false
    for _, standard_port := range config.ClientPorts {
      if client_port == standard_port {
        uses_standard_port = true
        break
      }
    }
    
    update_name := false
    update_ip := false
    cn := strings.ToLower(system.Text("cn"))
    if client_name != "none" && cn != client_name && cn != strings.SplitN(client_name,".",2)[0] {
      if !uses_standard_port {
        util.Log(1, "INFO! Client cn (%v) does not match DNS name (%v) but client runs on non-standard port (%v) => Assuming test and will not update cn", cn, new_name, client_port)
      } else if DoNotChangeCN(system) { 
        util.Log(1, "INFO! Client cn (%v) does not match DNS name (%v) but client is blacklisted for cn updates", cn, new_name)
      } else {
        util.Log(1, "INFO! Client cn (%v) does not match DNS name (%v) => Update cn", cn, new_name)
        update_name = true
      }
    }
    if client_ip != system.Text("iphostnumber") {
      if system.Text("iphostnumber") != "" && !uses_standard_port {
        util.Log(1, "INFO! Client ipHostNumber (%v) does not match IP (%v) but client runs on non-standard port (%v) => Assuming test and will not update ipHostNumber", system.Text("iphostnumber"), client_ip, client_port)
      } else {
        util.Log(1, "INFO! Client ipHostNumber (%v) does not match IP (%v) => Update ipHostNumber", system.Text("iphostnumber"), client_ip)
        update_ip = true
      }
    }
    
    if update_ip || update_name {
      system, err = db.SystemGetAllDataForMAC(macaddress, false) // need LDAP data without groups
      if system == nil {
        util.Log(0, "ERROR! LDAP error reading data for %v: %v", macaddress, err)
      } else {
        system_upd := system.Clone()
        if update_ip { system_upd.FirstOrAdd("iphostnumber").SetText(client_ip) }
        if update_name { system_upd.First("cn").SetText(new_name) }
        err = db.SystemReplace(system, system_upd)
        if err != nil {
          util.Log(0, "ERROR! LDAP error updating %v: %v", macaddress, err)
        }
      }
      
      system, err = db.SystemLocalPrinterForMAC(macaddress)
      if err != nil {
        if _, not_found := err.(db.SystemNotFoundError); !not_found {
          util.Log(0, "ERROR! LDAP error reading data for local printer for %v: %v", macaddress, err)
        }
      } else {
        system_upd := system.Clone()
        
        if update_ip { system_upd.FirstOrAdd("iphostnumber").SetText(client_ip) }
        
        if update_name {
          printer_name := system.Text("cn")
          if printer_name != cn {
            util.Log(1, "INFO! Local printer with MAC %v is called '%v', not '%v' => Will not rename it to '%v'", macaddress, printer_name, cn, new_name)
          } else {
            system_upd.First("cn").SetText(new_name)
          }
        }
        
        util.Log(1, "INFO! Updating cn and/or ipHostNumber of LDAP object for local printer for %v", macaddress)
        err = db.SystemReplace(system, system_upd)
        if err != nil {
          util.Log(0, "ERROR! LDAP error updating local printer for %v: %v", macaddress, err)
        }
      }
    }
  }
  
  // Launch all jobs for the new client that have a <tminus> with current time
  // after <timestamp>-<tminus>. This includes both local and foreign jobs.
  now_ts := util.MakeTimestamp(time.Now())
  affects_new_client := xml.FilterSimple("macaddress", macaddress)
  has_tminus := xml.FilterRegexp("tminus",".")
  var to_launch []xml.HashFilter
  jobs := db.JobsQuery(xml.FilterAnd([]xml.HashFilter{affects_new_client,has_tminus}))
  for child := jobs.FirstChild(); child != nil; child = child.Next() {
    job := child.Element()
    timestamp := job.Text("timestamp")
    tminus := job.Text("tminus")
    timestamp, err = util.AddTimestamp(timestamp, "-"+tminus)
    if err == nil {
      if now_ts >= timestamp {
        to_launch = append(to_launch, xml.FilterSimple("id",job.Text("id")))
      }
    }
  }
  
  if len(to_launch) > 0 {
    util.Log(1, "INFO! Launching jobs according to <tminus>")
    db.JobsModify(xml.FilterOr(to_launch), xml.NewHash("job","status","launch"))
  }
}

// Returns true if the CN of system must not be changed.
func DoNotChangeCN(system *xml.Hash) bool {
  cn := system.Text("cn")
  if strings.HasPrefix(cn, config.CNAutoPrefix) && strings.HasSuffix(cn, config.CNAutoSuffix) { return false }
  for i := range config.CNRenameBlacklist {
    if strings.HasSuffix(system.Text("dn"),config.CNRenameBlacklist[i]) { return true }
  }
  return false
}

// Returns true if less than 8s have passed since start.
// Otherwise logs a warning and returns false.
func checkTime(start time.Time, macaddress string) bool {
  if time.Since(start) < 8*time.Second { return true }
  util.Log(0, "WARNING! Could not complete registration of client %v within the time window", macaddress)
  return false 
}

func makeSureWeHaveAppropriateProcessingJob(macaddress, headertag, progress string) {
  job := xml.NewHash("job")
  job.Add("progress", progress)
  job.Add("status", "processing")
  job.Add("siserver", config.ServerSourceAddress)
  job.Add("targettag", macaddress)
  job.Add("macaddress", macaddress)
  job.Add("modified", "1")
  job.Add("timestamp", util.MakeTimestamp(time.Now()))
  job.Add("headertag", headertag)
  job.Add("result", "none")
  
  // Filter for selecting local jobs in status "processing" for the client's MAC.
  local_processing := xml.FilterSimple("siserver", config.ServerSourceAddress, "macaddress", macaddress, "status", "processing")
  
  // If we don't already have an appropriate job with status "processing", create one
  if db.JobsQuery(xml.FilterAnd([]xml.HashFilter{local_processing, xml.FilterSimple("headertag", headertag)})).FirstChild() == nil {
  
    // First cancel other local install or update jobs for the same MAC in status "processing",
    // because only one install or update job can be processing at any time.
    // NOTE: I'm not sure if clearing <periodic> is the right thing to do
    // in this case. See the corresponding note in foreign_job_updates.go
    install_or_update := xml.FilterOr([]xml.HashFilter{xml.FilterSimple("headertag", "trigger_action_reinstall"),xml.FilterSimple("headertag", "trigger_action_update")})
    local_processing_install_or_update := xml.FilterAnd([]xml.HashFilter{local_processing, install_or_update})
    db.JobsModifyLocal(local_processing_install_or_update, xml.NewHash("job","progress","groom")) // to prevent faistate => localboot
    db.JobsRemoveLocal(local_processing_install_or_update, true)
    
    // Now add the new job.
    db.JobAddLocal(job)
  }
}
