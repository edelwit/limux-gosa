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
         "time"
         "strconv"
         
         "../db"
         "../xml"
         "github.com/mbenkmann/golib/util"
         "github.com/mbenkmann/golib/deque"
         "../config"
         "../security"
       )

// Handles the message "gosa_query_jobdb".
//  xmlmsg: the decrypted and parsed message
//  context: the security context
// Returns:
//  unencrypted reply
func gosa_query_jobdb(xmlmsg *xml.Hash, context *security.Context) *xml.Hash {
  where := xmlmsg.First("where")
  if where == nil { where = xml.NewHash("where") }
  filter, err := xml.WhereFilter(where)
  if err != nil {
    util.Log(0, "ERROR! gosa_query_jobdb: Error parsing <where>: %v", err)
    filter = xml.FilterNone
  }
  
  // If necessary, wait for the effects of forwarded modify requests
  t_forward := db.MostRecentForwardModifyRequestTime.At(0).(time.Time)
  delay := config.GosaQueryJobdbMaxDelay - time.Since(t_forward)
  if delay > 0 { 
    util.Log(1, "INFO! Waiting %v before replying to gosa_query_jobdb to allow forwarded job modifications to take effect", delay)
    time.Sleep(delay)
  }
  
  filter = security.LimitFilter(filter, int64(context.Limits.MaxAnswers), context.PeerID.IP.String())

  jobdb_xml := db.JobsQuery(filter)
  
  // sort jobs
  cmp  := func(a,b interface{}) int {
    x  := a.(*xml.Hash)
    y  := b.(*xml.Hash)
    ts := x.Text("timestamp")+"00000000000000"
    // NOTE: The first component has only the 1st 10 digits of the timestamp.
    // This quantizes jobs by hour (i.e. ignoring minutes and seconds).
    // For this reason we have "timestamp" listed a 2nd time as the final component,
    // in that case including all digits.
    c1 := ts[0:10]+x.Text("headertag","status","plainname","macaddress","timestamp")
    ts  = y.Text("timestamp")+"00000000000000"
    c2 := ts[0:10]+y.Text("headertag","status","plainname","macaddress","timestamp")
    if c1 < c2 { return -1 }
    if c1 > c2 { return +1 }
    return 0
  }
  answers := deque.New()
  for child := jobdb_xml.FirstChild(); child != nil; child = child.Next() {
    answer := child.Remove()
    answers.InsertSorted(answer, cmp)
  }

  // maps IP:PORT to a string representation of that peer's downtime
  // the empty string represents a peer that is up
  downtime := map[string]string{config.ServerSourceAddress:""}
  
  // maps IP:PORT to server name
  servername := map[string]string{}
  
  for count := 0; count < answers.Count(); {
    answer := answers.At(count).(*xml.Hash)
    count++
    {
      siserver := answer.Text("siserver")
      
      // If we encounter this siserver for the first time,
      // get its downtime (if any) and cache it.
      if _, found := downtime[siserver] ; !found {
        dur := Peer(siserver).Downtime()
        if t := dur/time.Second; t == 0 {
          downtime[siserver] = ""
        } else {
          downtime[siserver] = verbalDuration(dur)
        }
      }
      
      // If the server is down, set status="error" and result=<error message>
      if downtime[siserver] != "" {    
        
        // Look up server name if we don't have it cached, yet.
        if _, have := servername[siserver] ; !have {
          servername[siserver] = siserver
          host,_,err := net.SplitHostPort(siserver)
          if err != nil {
            util.Log(0, "ERROR! SplitHostPort(%v): %v",siserver,err)
          } else {
            names, err := net.LookupAddr(host)
            if err != nil {
              util.Log(0, "ERROR! LookupAddr: %v",err)
            } else {
              if len(names) == 0 { names = []string{siserver} }
              servername[siserver] = names[0]
            }
          }
        }
        
        answer.FirstOrAdd("status").SetText("error")
        answer.FirstOrAdd("result").SetText("%v has been down for %v.",servername[siserver],downtime[siserver])
      }
       
      answer.Rename("answer"+strconv.FormatUint(uint64(count), 10))
      jobdb_xml.AddWithOwnership(answer)
    }
  }
  
  jobdb_xml.Add("header", "query_jobdb")
  jobdb_xml.Add("source", config.ServerSourceAddress)
  jobdb_xml.Add("target", xmlmsg.Text("source"))
  jobdb_xml.Add("session_id", "1")
  jobdb_xml.Rename("xml")
  return jobdb_xml
}

var unit_name = []string{"second","minute","hour","day","week","month","year","decade","century","millenium"}
var unit_name_plural = []string{"seconds","minutes","hours","days","weeks","months","years","decades","centuries","millenia"}
var unit_divisor = []float64{60  , 60   , 24  , 7   , 4.348125 , 12   , 10     ,   10    ,  10 }

func verbalDuration(dur time.Duration) string {
  t := float64(dur/time.Second)
  for i := range unit_divisor {
    p := t/unit_divisor[i]
    if p > 0.90 {
      t = p
      continue
    }
    
    switch {
      case t < 0.99: return fmt.Sprintf("almost 1 %v",unit_name[i])
      case t < 1.4: return fmt.Sprintf("more than 1 %v",unit_name[i])
      case t < 1.6: return fmt.Sprintf("1 1/2 %v",unit_name_plural[i])
      case t < 1.9: return fmt.Sprintf("more than 1 1/2 %v",unit_name_plural[i])
      case t < 1.99: return fmt.Sprintf("almost 2 %v",unit_name_plural[i])
      default: u := int(t+0.1)
               half := ""
               if int(t+0.6) > u { half = " 1/2" }
               return fmt.Sprintf("%d%v %v",u,half,unit_name_plural[i])
    }
  }
  
  return "ages"
}

