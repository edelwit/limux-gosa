/*
Copyright (c) 2012,2013 Landeshauptstadt München
Author: Matthias S. Benkmann

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

package main

import (
          "io"
          "io/ioutil"
          "os"
          "os/signal"
          "fmt"
          "net"
          "time"
          "bytes"
          "strconv"
          "strings"
          "syscall"
          "regexp"
          "crypto/tls"
          
          "../db"
          "../xml"
          "github.com/mbenkmann/golib/util"
          "github.com/mbenkmann/golib/deque"
          "../config"
          "../message"
          "../security"
       )

const VERSION_MESSAGE = `sibridge %v (revision %v)
Copyright (c) 2012,2013 Landeshauptstadt München
Author: Matthias S. Benkmann
This is free software; see the source for copying conditions.  There is NO
warranty; not even for MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.

`

const USAGE_MESSAGE = `USAGE: sibridge [args] [targetserver][:targetport]

Remote control for an siserver at targetserver:targetport.

--help       print this text and exit
--version    print version and exit

-v           print operator debug messages (INFO)
-vv          print developer debug messages (DEBUG)
             ATTENTION! developer messages include keys!

-c <file>    read config from <file> instead of default location
-l <port>    listen for socket connections on <port>
             Always uses TLS without STARTTLS (unlike go-susi).
             TLS client authentication is required. GOsa extensions
             with appropriate access control bits need to be present
             in the client certificate.
-e <string>  execute commands from <string>
-f <file>    execute commands from <file>. If <file> is not an ordinary
             file, it will be processed concurrently with other special
             files and data from other -e and -f arguments.
             This permits using FIFOs and other special files for input.

-i           Read from from stdin even if -l, -e or -f is used. Normally
             these switches suppress interactive mode.
`

const HELP_MESSAGE = `Basics:
  * Multiple commands per line are permitted if separated by ";"
  * Commands may be abbreviated to an arbitrary prefix (e.g. "wak" = "wakeup")
  * If a command is invoked without any machine arguments, the list of
    machines from the most recent command will be affected.
    E.g.: 
             examine m1 m2
             localboot
             wakeup
             
          sets both m1 and m2 to "localboot" and then wakes both of them up.

Argument types:
  Machine   - IP address, short name, fully qualified name, MAC address
  "*"       - (only for "query" and "delete") all machines with pending jobs
  Job type  - "update"/"softupdate", "reboot", "halt", "install"/"reinstall",
              "wakeup", "localboot", "lock", "unlock"/"activate",
              "send_user_msg"/"message"/"msg"
              These may be abbreviated to prefixes (e.g. "wak" = "wakeup" )
  date      - YYYY-MM-DD
  abs. time - HH:MM, H:M, HH:M, H:MM
  rel. time - a number followed by "s", "m", "h" or "d" for seconds, minutes,
              hours and days respectively. Relative times are always relative
              to the current time. I.e. "10m" means "10 minutes from now".
  substring - an arbitrary string that is used to select among a set of
              objects the one whose name contains the substring (case-insensitive).
              If there are multiple matches the object with the fewest additional
              characters is chosen.
              It is an error if no matching object is found.
  substrings - Multiple whitespace separated words that are used to select among
               a set of objects all those that match any of the substrings in the
               manner described for type "substring".
               It is an error if one of the substrings does not match an object.
  strings    - Multiple whitespace separated words whose meaning depends on
               the command.

Argument order:
  Times may either precede or follow the machines they should affect, 
  but the 2 styles cannot be mixed within the same command.
  E.g.: (Install machine1 and machine2 10 minutes from now and machine3 in 30)
                       install 10m machine1 machine2 30m machine3
    means the same as: install machine1 machine2 10m machine3 30m 
    But this is wrong: install machine1 machine2 10m 30m machine3
  
  The same applies to the job types that may be used with "query" and "delete".
  E.g.: (Query install jobs that affect machine1 or machine2)   
                        query i machine1 machine2
     means the same as: query machine1 machine2 i
  
Commands:
  help: Display this help.
  
  <job type>: Schedule job(s) of this type.
              Argument types: Machine, Date, Time

  raw:        Send arbitrary message to si-server.
              Argument types: strings
              Sends an arbitrary message to the si-server and prints the
              reply. All proper messages are of the form <xml>...</xml>
              but "raw" can be used to send malformed messages.
              
              If multiple words are passed as arguments and the first
              word following the raw command does not contain
              the character "<", it specifies either the config file
              section (e.g. "GOsaPackages") whose key is used to encrypt
              the message or the key itself. The command first checks if
              the word matches the name of an existing config file section
              with a key and if it doesn't uses the word directly.
              
              If no key/section is provided, the default is "GOsaPackages".
  
  encrypt:    Encrypt a message as appropriate for sending to an si-server.
              Argument types: strings
              Like the "raw" command (see above), but the encrypted
              message is printed instead of being sent to the si-server.
  
  decrypt:    Decrypt an si-server message.
              Argument types: strings
              The inverse of "encrypt" (see above). If no key is provided
              or decryption with the provide key fails, "decrypt" will try
              ALL keys found in the config file. If that fails, too, the
              encrypted message will be printed.
              NOTE: Decryption is only considered successfull if the result
              starts with "<xml>".
  
  kill:       Delete the LDAP object(s) of the selected machine(s).
              Argument types: Machine
              This command can not be abbreviated.
  
  foo-> :     Fill in missing LDAP attributes in selected machine(s).
              Argument types: Machine
              The missing attributes are copied from system "foo" 
              (a Machine as described in section "Argument Types" above). 
              
              If any of the selected machines are in ou=incoming, they
              will be moved into the same ou as "foo".
              The attribute gotoMode is never copied, so a locked system
              will remain locked, allowing you to make further changes
              before activating it for installation.
              
  .release:   Change the release of the selected machine(s).
              Argument types: substring
              
              This command does not take machines as argument.
              You must select the machines by using any other
              command (such as "examine") prior to ".release".
  
  .classes:   Set the FAI classes for the selected machine(s).
              Argument types: substrings
              Each substring selects exactly one FAI class.
              Only FAI classes available for the machine's release
              will be considered, so you should use the
              ".release" command first, if the release needs to be changed.
              
              This command does not take machines as argument.
              You must select the machines by using any other
              command (such as "examine") prior to ".classes".
  
  .deb, .repo: Set the list of repositories to use for the selected machine(s).
              Argument types: substrings
              Each substring is matched against the fairepository attributes of
              FAIrepositoryServer objects. The URL of the matching repository
              is extracted and becomes a FAIDebianMirror attribute value of the
              selected machine(s).
              Only repositories that have the proper release are considered,
              so you should use the ".release" command first if the
              release needs to be changed.
            
              This command does not take machines as argument.
              You must select the machines by using any other
              command (such as "examine") prior to ".deb".
  
  examine, x: Print info about machine(s).
              Argument types: Machine
              Client states: x_x o_o o_O ~_^ X_x ^_^ o_^ ^,^
              Server states: X_X O_O @_@ O_@ x_~ ^.^ @_~ ^_~
                        SSH:     yes     yes     yes     yes
                  si-client:         yes yes         yes yes
                  si-server:                 yes yes yes yes

  query_jobdb, query_jobs, jobs: 
              Query jobs matching the arguments.
              Argument types: Machine, "*", Job type
              NOTE:
                Using "*" does not clear the list of affected machines.
  
  delete_jobdb_entry, delete_jobs: 
              Delete jobs matching the arguments.
              Argument types: Machine, "*", Job type
              NOTE: 
                The "delete" command clears the list of affected machines.
  
  xx: Run "examine" command repeatedly until an empty line or new command.
      Argument types: Machine
  
  qq: Run "query" command repeatedly until an empty line or new command.
      Argument types: Machine, "*", Job type
`

// host:port of the siserver to talk to.
var TargetAddress = ""

// whether to start a listener for incoming TCP connections.
var ListenForConnections = false

// Force interactive mode even if batch commands given.
var Interactive = false

// All commands passed via -e and -f switches.
var BatchCommands bytes.Buffer

// Files passed via -f that are not ordinary files.
var SpecialFiles = []string{}

// nothing, SSH only, si-client only, SSH+si-client
// si-server + ...
var ClientStates = []string{"x_x", "o_o", "o_O", "~_^", "X_x", "^_^", "o_^", "^,^"}
var ServerStates = []string{"X_X", "O_O", "@_@", "O_@", "x_~", "^.^", "@_~", "^_~"}

var TimestampRE = regexp.MustCompile("^([0-9]{4})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})([0-9]{2})$")

func main() {
  config.ServerConfigPath = "/etc/gosa/gosa.conf"
  // This is NOT config.ReadArgs() !!
  ReadArgs(os.Args[1:])
  
  if len(os.Args) < 2 {
    config.PrintVersion = true
    config.PrintHelp = true
  }
  
  if config.PrintVersion {
    fmt.Printf(VERSION_MESSAGE, config.Version, config.Revision)
  }
  
  if config.PrintHelp {
    fmt.Println(USAGE_MESSAGE)
  }
  
  if config.PrintVersion || config.PrintHelp { os.Exit(0) }
  
  config.Init()
  ReadConfig() // This is NOT config.ReadConfig() !!
  config.ReadCertificates() // after ReadConfig()
  
  if config.TLSRequired && config.TLSServerConfig == nil {
    util.Log(0, "ERROR! No cert, no keys => no service")
    util.LoggersFlush(5*time.Second)
    os.Exit(1)
  }
  
  if TargetAddress == "" {
    TargetAddress = config.ServerSourceAddress
  }
  config.ServerSourceAddress = config.IP + config.ServerListenAddress[strings.Index(config.ServerListenAddress,":"):]
  
  util.LogLevel = config.LogLevel
  
  config.ReadNetwork() // after config.ReadConfig()
  config.Timeout = 20*time.Second
  config.FAIBase = db.LDAPFAIBase()
  
  target_reachable := make(chan bool, 2)
  go func() {
    conn, err := net.Dial("tcp", TargetAddress)
    if err != nil {
      util.Log(0, "ERROR! Dial(\"tcp\",%v): %v",TargetAddress,err)
      target_reachable <- false
    } else {
      conn.Close()
      target_reachable <- true
    }
  }()
    
  go func() {
    time.Sleep(250*time.Millisecond)
    target_reachable <- false
  }()
  
  if r := <-target_reachable; !r {
    os.Exit(1)
  }
  
  // If we support TLS, check if the target does, too
  // and mark it in the serverdb if it does.
  if config.TLSClientConfig != nil {
    // do not log errors for failed TLS connection attempt
    util.LogLevel = -1
    conn, _ := security.SendLnTo(TargetAddress, "", "", true)
    util.LogLevel = config.LogLevel
    if conn != nil { // TLS connection has succeeded
      conn.Close()
      server, err := util.Resolve(TargetAddress, config.IP)
      if err == nil {
        ip, port, err := net.SplitHostPort(server)
        if err == nil {
          source := ip + ":" + port
          server_xml := xml.NewHash("xml", "source", source)
          server_xml.Add("key", "") // key=="" is marker for TLS-support
          db.ServerUpdate(server_xml)
          util.Log(1, "INFO! %v (%v) supports TLS", TargetAddress, source)
        }
      }
    }
  }
  
  // Create channels for receiving events. 
  // The main() goroutine receives on all these channels 
  // and spawns new goroutines to handle the incoming events.
  connections := make(chan net.Conn,  32)
  signals     := make(chan os.Signal, 32)
  
  signals_to_watch := []os.Signal{ syscall.SIGUSR1, syscall.SIGUSR2, syscall.SIGHUP, syscall.SIGTTIN, syscall.SIGTTOU }
  signal.Notify(signals, signals_to_watch...)
  util.Log(1, "INFO! Intercepting these signals: %v", signals_to_watch)
  
  // Always treat target as go-susi to avoid side-effects from the
  // more complex protocol used to talk to gosa-si.
  message.Peer(TargetAddress).SetGoSusi(true)
  
  // Start a "connection" for the commands provided via -e and -f (ordinary files)
  if BatchCommands.Len() > 0 {
    connections <- NewReaderWriterConnection(&BatchCommands, Dup(syscall.Stdout,"BatchCommands:/dev/stdout"))
  }
  
  // Start connections for reading from special files
  for _, special := range SpecialFiles {
    file, err := os.Open(special)
    if err != nil {
      util.Log(0, "ERROR! Error opening \"%v\": %v", special, err)
    } else {
      connections <- NewReaderWriterConnection(file, Dup(syscall.Stdout,special+":/dev/stdout"))
    }
  }
  
  // Start a "connection" to Stdin/Stdout for interactive use
  var interactive_conn net.Conn
  if Interactive || (!ListenForConnections && BatchCommands.Len()==0) {
    interactive_conn = NewReaderWriterConnection(Dup(syscall.Stdin,"interactive:/dev/stdin"), Dup(syscall.Stdout,"interactive:/dev/stdout"))
    connections <- interactive_conn
  }
  
  // If requested, accept TCP connections
  if ListenForConnections {
    if config.TLSServerConfig == nil {
      util.Log(0, "ERROR! -l option requires TLS certificates to be configured")
      util.LoggersFlush(5*time.Second)
      os.Exit(1)
    }
    tcp_addr, err := net.ResolveTCPAddr("tcp4", config.ServerListenAddress)
    if err != nil {
      util.Log(0, "ERROR! ResolveTCPAddr: %v", err)
      os.Exit(1)
    }

    listener, err := net.ListenTCP("tcp4", tcp_addr)
    if err != nil {
      util.Log(0, "ERROR! ListenTCP: %v", err)
      os.Exit(1)
    }
    util.Log(1, "INFO! Accepting connections on %v", tcp_addr);
    go acceptConnections(listener, connections)
  }
  
  connectionTracker := deque.New()
  go func() {
    for {
      connectionTracker.WaitForItem(0)
      connectionTracker.WaitForEmpty(0)
      if !ListenForConnections { 
        util.Log(1, "INFO! Last connection closed => Terminating")
        config.Shutdown() // delete tempdir
        util.LoggersFlush(5*time.Second)
        os.Exit(0) 
      }
    }
  }()
  
  /********************  main event loop ***********************/  
  for{ 
    select {
      case sig := <-signals : //os.Signal
                    if sig == syscall.SIGTTIN || sig == syscall.SIGTTOU {
                      if interactive_conn != nil { // to avoid getting the log message multiple times
                        util.Log(1, "INFO! Received signal \"%v\" => Closing console", sig)
                        interactive_conn.Close()
                        interactive_conn = nil
                      }
                    } else {
                      util.Log(1, "INFO! Received signal \"%v\"", sig)
                    }
                    
      case conn:= <-connections : // net.Conn
                    util.Log(1, "INFO! Incoming TCP request from %v", conn.RemoteAddr())
                    go util.WithPanicHandler(func(){handle_request(conn, connectionTracker)})
    }
  }
}

// Accepts TCP connections on listener and sends them on the channel connections.
func acceptConnections(listener *net.TCPListener, connections chan<- net.Conn) {
  for {
    tcpConn, err := listener.AcceptTCP()
    if err != nil { 
      util.Log(0, "ERROR! AcceptTCP: %v", err) 
    } else {
      err = tcpConn.SetKeepAlive(true)
      if err != nil {
        util.Log(0, "ERROR! SetKeepAlive: %v", err)
      }
      conn := tls.Server(tcpConn, config.TLSServerConfig)
      connections <- conn
    }
  }
}

// Handles one or more messages received over conn. Each message is a single
// line terminated by \n.
func handle_request(conn net.Conn, connectionTracker *deque.Deque) {
  connectionTracker.Push(true)
  defer connectionTracker.Pop()
  defer conn.Close()
  defer util.Log(1, "INFO! Connection to %v closed", conn.RemoteAddr())
  
  var err error
  
  var context *security.Context
  switch conn.(type) {
    case *tls.Conn: context = security.ContextFor(conn)
                    if context == nil { return }
    default: context = &security.Context{}
             security.SetLegacyDefaults(context)
  }

  // If the user does not specify any machines in the command,
  // the list of machines from the previous command will be used.
  // The following slice is passed via pointer with every call of
  // processMessage() so that each call can access the previous call's data
  jobs := []jobDescriptor{}
  
  util.SendLn(conn, "# Enter \"help\" to get a list of commands.\n# Ctrl-D terminates the connection.\n", config.Timeout)
  
  repeat := time.Duration(0)
  repeat_command := ""
  var buf = make([]byte, 65536)
  i := 0
  n := 1
  for n != 0 {
    util.Log(2, "DEBUG! Receiving from %v", conn.RemoteAddr())
    
    var deadline time.Time // zero value means "no deadline"
    if repeat > 0 { deadline = time.Now().Add(repeat) }
    conn.SetReadDeadline(deadline)
    n, err = conn.Read(buf[i:])
    if neterr,ok := err.(net.Error); ok && neterr.Timeout() {
      n = copy(buf[i:], repeat_command)
      err = nil
    }
    
    repeat = 0  
    i += n
    
    if err != nil && err != io.EOF {
      util.Log(0, "ERROR! Read: %v", err)
    }
    if err == io.EOF {
      util.Log(2, "DEBUG! Connection closed by %v", conn.RemoteAddr())
      // make sure the data is \n terminated
      buf = append(buf, '\n') // in case i == len(buf)
      buf[i] = '\n'
      i++
    }
    if n == 0 && err == nil {
      util.Log(0, "ERROR! Read 0 bytes but no error reported")
    }
    
    if i == len(buf) {
      buf_new := make([]byte, len(buf)+65536)
      copy(buf_new, buf)
      buf = buf_new
    }

    // Replace ";" with "\n" to support multiple commands on one line
    for k := 0; k < i; k++ {
      if buf[k] == ';' { buf[k] = '\n' }
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
      message := strings.TrimSpace(string(buf[start:start+eol]))
      start += eol+1
      if message != "" { // ignore empty lines
        var reply string
        reply,repeat = processMessage(message, &jobs, context)
        repeat_command = message + "\n"
        
        // if we already have more data, cancel repeat immediately
        if start < i { repeat = 0 }
        
        if reply != "" {
          util.Log(2, "DEBUG! Sending reply to %v: %v", conn.RemoteAddr(), reply)
          util.SendLn(conn, reply, config.Timeout)
        }
      }
    }
  }
}

var jobs      = []string{"update","softupdate","reboot","halt","install",  "reinstall","wakeup","localboot","lock","unlock",  "activate", "send_user_msg","msg",         "message"}
// It's important that the jobs are at the beginning of the commands slice,
// because we use that fact later to distinguish between commands that refer to
// jobs and other commands.
var commands  = append(jobs,                                                                                                                                                             "help","x",      "examine", "query_jobdb","query_jobs","jobs", "delete_jobs","delete_jobdb_entry","qq","xx","kill", ".release", ".classes", ".debianrepository", ".repository", "raw", "encrypt", "decrypt")
var canonical = []string{"update","update"    ,"reboot","halt","reinstall","reinstall",  "wake","localboot","lock","activate","activate","send_user_msg","send_user_msg","send_user_msg","help","examine","examine", "query",      "query",     "query","delete",     "delete"            ,"qq","xx","kill", ".release", ".classes", ".deb"             , ".deb"       , "raw", "encrypt", "decrypt"}

type jobDescriptor struct {
  MAC string
  IP string
  Name string // "*" means all machines (only valid for some commands like "query")
  Date string
  Time string
  Job string
  Sub string
}

func (j *jobDescriptor) HasMachine() bool { return j.MAC != "" }
func (j *jobDescriptor) HasJob() bool { return j.Job != "" }
func (j *jobDescriptor) HasDate() bool { return j.Date != "" }
func (j *jobDescriptor) HasTime() bool { return j.Time != "" }
func (j *jobDescriptor) HasSub() bool { return j.Sub != "" }

const PERMISSION_DENIED = "! PERMISSION DENIED"

// msg must be non-empty.
// joblist: see comment in handle_request() for explanation
//
// Returns:
//  reply: text to send back to the requestor
//  repeat: if non-0, if the requestor does not send anything within that time, repeat the same command
func processMessage(msg string, joblist *[]jobDescriptor, context *security.Context) (reply string, repeat time.Duration) {
  fields := strings.Fields(msg)
  
  idx := strings.Index(fields[0],"->")
  if idx > 0 {
    msg = msg[0:idx]+" "+msg[idx:]
    fields = strings.Fields(msg)
  }
  
  if len(fields) > 1 && strings.HasPrefix(fields[1],"->") {
    fields[0] += "->"
    fields[1] = fields[1][2:]
    if fields[1] == "" { fields = strings.Fields(strings.Join(fields," ")) }
  }
  
  cmd := strings.ToLower(fields[0]) // always present because msg is non-empty
  
  i := 0
  is_job_cmd := false
  
  var sys_to_copy *xml.Hash
  
  if strings.HasSuffix(cmd,"->") {
    // Early permissions check to avoid all the database queries if
    // the client does not have permission to use the command anyway.
    if !context.Access.LDAPUpdate.DH || !context.Access.DetectedHW.DN {
      return PERMISSION_DENIED, 0
    }
    template := jobDescriptor{}
    if !parseMachine(cmd[0:len(cmd)-2], &template) {
      return "! Cannot find system to copy: "+cmd, 0
    }
    cmd = "copy"
    sys_to_copy, _ = db.SystemGetAllDataForMAC(template.MAC, false)
    if sys_to_copy == nil { return "! Can't happen", 0 }
    
  } else {
    for ; i < len(commands); i++ {
      
      // The "kill" command can not be abbreviated for safety reasons.
      if commands[i] == "kill" {
        if cmd == "kill" { break }
        continue
      }
      
      if strings.HasPrefix(commands[i], cmd) { break }
    }
    
    if i == len(commands) {
      return "! Unrecognized command: " + cmd, 0
    }
    
    // cmd is the canonical name for the command, e.g. if the user entered "x"
    // then cmd is now "examine".
    cmd = canonical[i]
    
    // As explained in the command at var commands, determine if the command is a job.
    is_job_cmd = (i < len(jobs))
  }
  
  // Depending on the type of command, only certain kinds of arguments are permitted:
  //  all non-dot commands (except "raw"): machine references (MAC, IP, name)
  //  job commands: times (XXs, XXm, XXh, XXd, YYYY-MM-DD, HH:MM)
  //  delete: job type ("update","softupdate","reboot","halt","install", "reinstall","wakeup","localboot","lock","unlock", "activate")
  //  query,qq and delete: all machines wildcard "*"
  //  dot commands and "raw": substrings
  allowed := map[string]bool{"machine":true}
  if is_job_cmd { allowed["time"] = true }
  if cmd == "delete" { allowed["job"]=true }
  if cmd == "delete" || cmd == "query" || cmd == "qq" { allowed["*"]=true }
  if cmd[0] == '.' || cmd == "raw" || cmd == "encrypt" || cmd == "decrypt" { allowed["substring"]=true; allowed["machine"]=false }
  
  // parse all fields into partial job descriptors
  parsed := []jobDescriptor{}
  for i=1; i < len(fields); i++ {
    template := jobDescriptor{}
    
    if (allowed["time"] && parseTime(fields[i], &template)) ||
      // test machine names before jobs. Otherwise many valid machine names such as "rei" would
      // be interpreted as job types ("reinstall" in the example)
       (allowed["machine"] && parseMachine(strings.ToLower(fields[i]), &template)) ||
       (allowed["job"] && parseJob(strings.ToLower(fields[i]), &template)) ||
       (allowed["*"] && parseWild(strings.ToLower(fields[i]), &template)) ||
       (allowed["substring"] && parseSubstring(fields[i], &template)) {
      parsed = append(parsed, template)
      continue 
    } else 
    {
      return "! Illegal argument: "+fields[i],0
    }
  }
  
  // Some people consider it more intuitive to list machines before times/job types
  // and others consider the reverse order more intuitive, e.g.
  //   "delete dev3 install"  vs  "delete install dev3"
  //   "install dev3 10:30"   vs  "install 10:30 dev3"
  // We try to understand both by checking if a machine reference is listed before
  // a time or job type and in that case we simply reverse the list.
  last_machine_ref := len(parsed)-1
  last_other := len(parsed)-1
  for ; last_machine_ref >= 0; last_machine_ref-- {
    if parsed[last_machine_ref].HasMachine() { break }
  }
  for ; last_other >= 0; last_other-- {
    if !parsed[last_other].HasMachine() { break }
  }
  if last_machine_ref >= 0 && last_other > last_machine_ref {
    for i:=0; i < len(parsed)>>1; i++ { 
      parsed[i],parsed[len(parsed)-1-i] = parsed[len(parsed)-1-i], parsed[i]
    }
  }
  
  // If the fields contain no non-wildcard machine references, append them
  // from the previous job list.
  have_machine := false
  for i = range parsed { 
    if parsed[i].Name != "" && parsed[i].Name != "*" { have_machine = true }
  }
  if !have_machine {
    for _, j := range *joblist {
      if j.Name != "*" { 
        jd := jobDescriptor{Name:j.Name, MAC:j.MAC, IP:j.IP}
        parsed = append(parsed, jd)
      }  
    }
  }
  
  // Now merge the fields into a new job list
  now := util.MakeTimestamp(time.Now())
  template := jobDescriptor{Date:now[0:8], Time:now[8:]}
  *joblist = []jobDescriptor{}
  for _, j := range parsed {
    if j.HasJob() {
      template.Job = j.Job
    }
    if j.HasSub() {
      if template.Sub == "" {
        template.Sub = j.Sub 
      } else {
        template.Sub += " "+j.Sub 
      }
    }
    if j.HasDate() {
      template.Date = j.Date
    }
    if j.HasTime() {
      template.Time = j.Time
    }
    if j.HasMachine() {
      j.Date = template.Date
      j.Time = template.Time
      j.Job = template.Job
      j.Sub = template.Sub
      *joblist = append(*joblist, j)
    }
  }
  
  reply = ""
  repeat = 0
  
  util.Log(2, "DEBUG! Handling command \"%v\"", cmd)
  
  if is_job_cmd {
    for k := range *joblist { (*joblist)[k].Job = cmd }
    reply = commandJob(joblist, context)
  } else if cmd == "help" {
    reply = HELP_MESSAGE
  } else if cmd == "qq" {
    if context.Access.Query.QueryJobs || context.Access.Query.QueryAll {
      reply = commandGosa("gosa_query_jobdb", false,joblist)
      repeat = 5*time.Second
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "xx" {
    if context.Access.Query.QueryAll {
      reply = commandExamine(joblist)
      repeat = 2*time.Second
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "examine" {
    if context.Access.Query.QueryAll {
      reply = commandExamine(joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "query" {
    if context.Access.Query.QueryJobs || context.Access.Query.QueryAll {
      reply = commandGosa("gosa_query_jobdb",false,joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "raw" {
    if context.Access.Misc.Debug {
      reply = commandRaw(template.Sub, 0)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "encrypt" {
    reply = commandRaw(template.Sub, 1)
  } else if cmd == "decrypt" {
    reply = commandRaw(template.Sub, 2)
  } else if cmd == "kill" {
    if context.Access.LDAPUpdate.DH && context.Access.DetectedHW.DN {
      reply = commandKill(joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "copy" {
    if context.Access.LDAPUpdate.DH && context.Access.DetectedHW.DN { // we did this check earlier, but for completeness' sake we have it here, too.
      reply = commandCopy(sys_to_copy, joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == ".release" {
    if context.Access.LDAPUpdate.DH {
      reply = commandRelease(joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == ".classes" {
    if context.Access.LDAPUpdate.DH {
      reply = commandClasses(joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == ".deb" {
    if context.Access.LDAPUpdate.DH {
      reply = commandDeb(joblist)
    } else {
      reply = PERMISSION_DENIED
    }
  } else if cmd == "delete" {
    if context.Access.Jobs.ModifyJobs || context.Access.Jobs.JobsAll {
      reply = strings.Replace(commandGosa("gosa_query_jobdb",true,joblist),"==","<-",-1)+"\n"+
            commandGosa("gosa_delete_jobdb_entry",true,joblist)
    } else {
      reply = PERMISSION_DENIED
    } 
    *joblist = []jobDescriptor{} // reset selected machines
  }
  
  return reply,repeat
}

func commandRelease(joblist *[]jobDescriptor) (reply string) {
  db.FAIReleasesListUpdate()
  releases := db.FAIReleases()
  
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    if j.Sub  == ""  { continue }
    
    if reply != "" { reply += "\n" }
    
    best_release := ""
    best_score := 19770120
    multi := false
    for _, r := range releases {
      if len(r) - len(j.Sub) > best_score { continue }
      if strings.Contains(strings.ToLower(r),strings.ToLower(j.Sub)) {
        new_score := len(r) - len(j.Sub)
        if new_score == best_score { 
          multi = true 
          best_release += ", " + r
        } else {
          multi = false
          best_release = r
        }
        best_score = new_score
      }
    }
    
    if multi {
      reply += "! ERROR: Multiple matches for \""+j.Sub+"\": " + best_release
      continue
    }
    
    if best_release == "" {
      reply += fmt.Sprintf("! ERROR: No matches for \"%v\". Candidates: %v", j.Sub,releases)
      continue
    }
    
    faiclass := db.SystemGetState(j.MAC, "faiclass")
    
    idx := strings.Index(faiclass, ":")
    if idx >= 0 { faiclass = faiclass[0:idx] } // remove old release from faiclass
    
    faiclass += ":" + best_release
    
    err := db.SystemSetStateMulti(j.MAC, "faiclass", []string{faiclass})
    if err != nil {
      reply += err.Error()
    } else {
      reply += "UPDATED " + j.Name + " ("+j.MAC+")"
    }
    
    reply += "\n" + examine(&j)
  }
  return reply
}

func commandClasses(joblist *[]jobDescriptor) (reply string) {
  mainloop:
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    if j.Sub  == ""  { continue }
    
    if reply != "" { reply += "\n" }
    
    faiclass := db.SystemGetState(j.MAC, "faiclass")
    
    idx := strings.Index(faiclass, ":")
    if idx < 0 { 
      reply += "! ERROR: Could not determine release of "+j.Name+" ("+j.MAC+")"
      continue mainloop
    }
    
    release := faiclass[idx+1:]
    faiclass = ""
    
    classes := db.FAIClasses(xml.FilterSimple("fai_release", release))

    for _, sub := range strings.Fields(j.Sub) {
      best_class := ""
      best_score := 19770120
      multi := false
      for c := classes.FirstChild(); c != nil; c = c.Next() {
        name := c.Element().Text("class")
        if len(name) - len(sub) > best_score { continue }
        if strings.Contains(strings.ToLower(name),strings.ToLower(sub)) {
          new_score := len(name) - len(sub)
          if new_score == best_score && name != best_class { 
            multi = true 
            best_class += ", " + name
          } else {
            multi = false
            best_class = name
          }
          best_score = new_score
        }
      }
      
      if multi {
        reply += "! ERROR: Multiple matches for \""+sub+"\": " + best_class
        continue mainloop
      }
      
      if best_class == "" {
        reply += fmt.Sprintf("! ERROR: No matches for \"%v\" in release \"%v\".", sub, release)
        continue mainloop
      }
      
      if faiclass != "" { 
        faiclass += " " 
      }
      faiclass += best_class
    }
    
    faiclass += " :" + release
    
    err := db.SystemSetStateMulti(j.MAC, "faiclass", []string{faiclass})
    if err != nil {
      reply += err.Error()
    } else {
      reply += "UPDATED " + j.Name + " ("+j.MAC+")"
    }
    
    reply += "\n" + examine(&j)
  }
  return reply
}

func commandDeb(joblist *[]jobDescriptor) (reply string) {
  mainloop:
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    if j.Sub  == ""  { continue }
    
    if reply != "" { reply += "\n" }
    
    faiclass := db.SystemGetState(j.MAC, "faiclass")
    
    idx := strings.Index(faiclass, ":")
    if idx < 0 { 
      reply += "! ERROR: Could not determine release of "+j.Name+" ("+j.MAC+")"
      continue mainloop
    }
    
    release := faiclass[idx+1:]
    
    servers := db.FAIServers()
    repos := []string{}

    for _, sub := range strings.Fields(j.Sub) {
      best_repo := ""
      best_score := 19770120
      multi := false
      for srv := servers.FirstChild(); srv != nil; srv = srv.Next() {
        if srv.Element().Text("fai_release") != release { continue }
        repo := srv.Element().Text("server")
        if len(repo) - len(sub) > best_score { continue }
        if strings.Contains(strings.ToLower(repo),strings.ToLower(sub)) {
          new_score := len(repo) - len(sub)
          if new_score == best_score && repo != best_repo { 
            multi = true 
            best_repo += ", " + repo
          } else {
            multi = false
            best_repo = repo
          }
          best_score = new_score
        }
      }
      
      if multi {
        reply += "! ERROR: Multiple matches for \""+sub+"\": " + best_repo
        continue mainloop
      }
      
      if best_repo == "" {
        reply += fmt.Sprintf("! ERROR: No matches for \"%v\" with release \"%v\".", sub, release)
        continue mainloop
      }
      
      repos = append(repos, best_repo)
    }
    
    err := db.SystemSetStateMulti(j.MAC, "faidebianmirror", repos)
    if err != nil {
      reply += err.Error()
    } else {
      reply += "UPDATED " + j.Name + " ("+j.MAC+")"
    }
    
    reply += "\n" + examine(&j)
  }
  return reply
}

func commandJob(joblist *[]jobDescriptor, context *security.Context) (reply string) {
  reply = ""
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    
    if reply != "" {reply = reply + "\n" }
    reply = reply + fmt.Sprintf("=> %-10v %v  %v (%v)\n", j.Job, util.ParseTimestamp(j.Date+j.Time).Format("2006-01-02 15:04:05"), j.MAC, j.Name)
    header := "job_trigger_action_" + j.Job
    if j.Job == "send_user_msg" { header = "job_" + j.Job }
    xmlmess := fmt.Sprintf("<xml><header>%v</header><source>GOSA</source><target>%v</target><macaddress>%v</macaddress><timestamp>%v</timestamp></xml>", header, j.MAC, j.MAC, j.Date+j.Time)
    permitted := false
    switch j.Job {
      case "lock":     permitted = context.Access.Jobs.Lock || context.Access.Jobs.JobsAll
      case "activate": permitted = context.Access.Jobs.Unlock || context.Access.Jobs.JobsAll
      case "reboot",
           "halt":     permitted = context.Access.Jobs.Shutdown || context.Access.Jobs.JobsAll
      case "wake":     permitted = context.Access.Jobs.Wake || context.Access.Jobs.JobsAll
      case "localboot":permitted = context.Access.Jobs.Abort || context.Access.Jobs.JobsAll
      case "reinstall":permitted = context.Access.Jobs.Install || context.Access.Jobs.JobsAll
      case "update":   permitted = context.Access.Jobs.Update || context.Access.Jobs.JobsAll
      case "send_user_msg":permitted = context.Access.Jobs.UserMsg || context.Access.Jobs.JobsAll
    }
    if permitted {
      gosa_reply := <- message.Peer(TargetAddress).Ask(xmlmess, config.ModuleKey["[GOsaPackages]"])
      reply += parseGosaReply(gosa_reply)
    } else {
      reply += PERMISSION_DENIED
    }
  }
  if reply == "" { reply = "NO JOBS" }
  return reply
}

// + active 1c:6f:65:08:b5:4d (nova) "localboot" :plophos
// - active 1c:6f:65:08:b5:4d (nova) "localboot" :plophos/4.1.0
func commandExamine(joblist *[]jobDescriptor) (reply string) {
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    
    if reply != "" { reply += "\n" }
    reply += examine(&j)
  }
  
  return reply
}

func examine(j *jobDescriptor) (reply string) {
    ports := []string{"22","20083","20081"}
    reachable := []chan int{make(chan int, 2),make(chan int, 2),make(chan int, 2)}
    for i := range ports {
      go func(port string, c chan int) {
        conn, err := net.Dial("tcp", j.IP+":"+port)
        if err != nil {
          c <- 0
        } else {
          conn.Close()
          c <- 1
        }
      }(ports[i],reachable[i])
    }
    
    go func() {
      time.Sleep(250*time.Millisecond)
      for i := range reachable { reachable[i] <- 0 }
    }()
    
    sys, err := db.SystemGetAllDataForMAC(j.MAC, true)
    if sys == nil { 
      reply += err.Error()
      return reply
    }
        
    grps := db.SystemGetGroupsWithMember(sys.Text("dn"))
    gotomode := sys.Text("gotomode")
    faistate := sys.Text("faistate")
    faiclass := sys.Text("faiclass")
    release := "unknown"
    if strings.Index(faiclass,":")>=0 { release = faiclass[strings.Index(faiclass,":"):] }
    
    if db.SystemIsWorkstation(j.MAC) {
      reply += ClientStates[<-reachable[0]+ <-reachable[1]*2 + <-reachable[2]*4]
    } else {
      reply += ServerStates[<-reachable[0]+ <-reachable[1]*2 + <-reachable[2]*4]
    }
    reply += " "
    reply += fmt.Sprintf("%v %v (%v) \"%v\" %v",gotomode,j.MAC,j.Name,faistate,release)
    for _,class := range strings.Fields(faiclass) {
      if class[0] == ':' { continue }
      reply += " " + class
    }
    if grps.FirstChild() != nil {
      reply += "\n    inherits from:"
      for g := grps.FirstChild(); g != nil; g = g.Next() {
        reply += " " + g.Element().Text("cn")
      }
    }
    for mirror := sys.First("faidebianmirror"); mirror != nil; mirror = mirror.Next() {
      reply += "\n    " + mirror.Text()
    }
    for ldaps := sys.First("gotoldapserver"); ldaps != nil; ldaps = ldaps.Next() {
      ldap := ldaps.Text()
      if strings.Index(ldap,":") >= 0 { ldap = ldap[strings.Index(ldap,":")+1:] }
      if strings.Index(ldap,":") >= 0 { ldap = ldap[strings.Index(ldap,":")+1:] }
      reply += "\n    " + ldap
    }
    for repos := sys.First("fairepository"); repos != nil; repos = repos.Next() {
      repo := repos.Text()
      repo_parts := strings.Split(repo,"|")
      reply += "\n    offers: " + repo_parts[2] + " " + repo_parts[3] + " \tURL: "+repo_parts[0]
    }

    return reply
}

func commandKill(joblist *[]jobDescriptor) (reply string) {
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    
    if reply != "" { reply += "\n" }
    sys, err := db.SystemGetAllDataForMAC(j.MAC, false)
    if sys == nil { 
      reply += err.Error()
      continue 
    }
    
    err = db.SystemReplace(sys, nil)
    if err != nil {
      reply += err.Error()
    } else {
      reply += "DELETED " + sys.Text("dn")
    }
  }
  return reply
}

func commandCopy(template *xml.Hash, joblist *[]jobDescriptor) (reply string) {
  for _, j := range *joblist {
    if j.Name == "*" { continue }
    
    if reply != "" { reply += "\n" }
    sys, err := db.SystemGetAllDataForMAC(j.MAC, false)
    if sys == nil { 
      reply += err.Error()
      continue 
    }

    newsys := sys.Clone()
    
    if strings.HasSuffix(sys.Text("dn"), config.IncomingOU) {
      newsys.RemoveFirst("dn") // so that a new one will be filled in from the template
    }
      
    // If necessary db.SystemFillInMissingData() also generates a dn 
    // derived from system's cn and template's dn.
    db.SystemFillInMissingData(newsys, template)
    
    if sys.Text("gotomode") != "active" {
      newsys.FirstOrAdd("gotomode").SetText("locked")
    }
      
    err = db.SystemReplace(sys, newsys)
    if err != nil {
      reply += err.Error()
    } else {
      reply += "UPDATED " + newsys.Text("dn")
    }
      
    // Add system to the same object groups template is member of (if any).
    db.SystemAddToGroups(newsys.Text("dn"), db.SystemGetGroupsWithMember(template.Text("dn")))
    
    reply += "\n" + examine(&j)
  }
  return reply
}

// This difficult function is only necessary because stupid gosa-si requires queries to be in CNF.
// So we need to convert our DNF jobDescriptors into long and ugly CNF clauses.
func generate_clauses(joblist *[]jobDescriptor, idx int, machines *map[string]bool, jobtypes *map[string]bool, clauses *string) {
  if idx == len(*joblist) {
    if len(*machines) > 0 || len(*jobtypes) > 0 {
      *clauses = *clauses + "<clause><connector>or</connector>"
      for m := range *machines {
        *clauses = *clauses + "<phrase><macaddress>"+m+"</macaddress></phrase>"
      }
      for j := range *jobtypes {
        header := j
        if j != "send_user_msg" { header = "trigger_action_"+header}
        *clauses = *clauses + "<phrase><headertag>"+header+"</headertag></phrase>"
      }
      *clauses = *clauses + "</clause>"
    }
  } else {
    job := (*joblist)[idx]
    if job.Name == "*" && !job.HasJob() {
      // do nothing. Don't even recurse because this is an always true case
      // In fact if this case is encountered we could abort the whole generate_clauses because
      // it must end up being empty.
    } else if job.Name != "*" && job.HasJob() {
      // We can optimize away one branch of the recursion if it doesn't add anything new,
      // but we must not trim both, because we must recurse to i==len(*joblist) for the
      // clause to be generated.
      
      one_branch_done := false
      if !(*jobtypes)[job.Job] {
        (*jobtypes)[job.Job] = true
        generate_clauses(joblist, idx+1, machines, jobtypes, clauses)
        delete(*jobtypes, job.Job)
        one_branch_done = true
      }
      
      have_machine := (*machines)[job.MAC]
      if !have_machine || !one_branch_done {
        (*machines)[job.MAC] = true
        generate_clauses(joblist, idx+1, machines, jobtypes, clauses)
        if !have_machine { delete(*machines, job.MAC) }
      }
    } else { // if either job.Name != "*" or job.HasJob() but not both
      if job.HasJob() {
        have_type := (*jobtypes)[job.Job]
        (*jobtypes)[job.Job] = true
        generate_clauses(joblist, idx+1, machines, jobtypes, clauses)
        if !have_type { delete(*jobtypes, job.Job) }
      } else {
        have_machine := (*machines)[job.MAC]
        (*machines)[job.MAC] = true
        generate_clauses(joblist, idx+1, machines, jobtypes, clauses)
        if !have_machine { delete(*machines, job.MAC) }
      }
    }
  }
}

func commandGosa(header string, use_job_type bool, joblist *[]jobDescriptor) (reply string) { 
  clauses := ""
  if use_job_type {
    machines := map[string]bool{}
    jobtypes := map[string]bool{}
    generate_clauses(joblist, 0, &machines, &jobtypes, &clauses)
  } else {
    for _, job := range *joblist {
      if job.Name == "*" { clauses = "" ; break }
      clauses = clauses + "<phrase><macaddress>"+job.MAC+"</macaddress></phrase>"
    }
    
    if clauses != "" {
      clauses = "<clause><connector>or</connector>" + clauses + "</clause>"
    }
  }

  gosa_cmd := "<xml><header>"+header+"</header><source>GOSA</source><target>GOSA</target><where>"+clauses+"</where></xml>"
  reply = <- message.Peer(TargetAddress).Ask(gosa_cmd, config.ModuleKey["[GOsaPackages]"])
  return parseGosaReply(reply)
}

func commandRaw(line string, mode int) (reply string) { 
  // The first word is the key, unless it contains a "<". In that case
  // we assume that the XML message contains spaces and there is no key.
  // This means that key strings containing "<" can not be used with this function.
  f := strings.Fields(line)
  key := f[0]
  if len(f) == 1 || strings.Contains(key, "<") {
    key = ""
  }
  gosa_cmd := strings.TrimSpace(line[len(key):])

  if key == "" { key = "GOsaPackages" }
  module_key, is_module_key := config.ModuleKey["["+key+"]"]
  if is_module_key { key = module_key }
  if mode == 0 {
    reply = <- message.Peer(TargetAddress).Ask(gosa_cmd, key)
  } else if mode == 1 {
    reply = security.GosaEncrypt(gosa_cmd, key)
  } else if mode == 2 {
    reply = security.GosaDecrypt(gosa_cmd, key)
    if reply == "" {
      for _, key := range config.ModuleKeys {
        reply = security.GosaDecrypt(gosa_cmd, key)
        if reply != "" { break }
      }
    }
    if reply == "" { reply = gosa_cmd }
  }
  return reply
}


func parseGosaReply(reply_from_gosa string) string {
  x, err := xml.StringToHash(reply_from_gosa)
  if err != nil { return fmt.Sprintf("! %v",err) }
  if x.First("error_string") != nil { return fmt.Sprintf("! %v", x.Text("error_string")) }
  if x.First("answer1") == nil { return "NO MATCH" }
  if x.Text("answer1") == "0" || 
      // workaround for gosa-si bug
     strings.HasPrefix(x.Text("answer1"),"ARRAY") { return "OK" }
  
  reply := ""
  for child := x.FirstChild(); child != nil; child = child.Next() {
    if !strings.HasPrefix(child.Element().Name(), "answer") { continue }
    answer := child.Element()
    if reply != "" {reply = reply + "\n" }
    job := answer.Text("headertag")
    if strings.Index(job, "trigger_action_") == 0 { job = job[15:] }
    if job == "send_user_msg" { job = "message" }
    progress := answer.Text("progress")
    status := (answer.Text("status")+"    ")[:4]
    if status == "proc" {
      if progress != "" && progress != "none" {
        status = progress+"%"
      }
    } else {
      if progress != "" && progress != "none" {
        status += "("+progress+"%)"
      }
    }
    periodic := answer.Text("periodic")
    if periodic == "none" { periodic = "" }
    if periodic != "" {
      periodic = " repeated every " + strings.Replace(periodic, "_", " ",-1)
    }
    handler := ""
    siserver := answer.Text("siserver")
    if siserver != "localhost" && siserver != x.Text("source") {
      siserver = strings.Split(siserver,":")[0]
      handler = db.SystemNameForIPAddress(siserver)
      if handler == "none" { handler = siserver }
      handler = strings.Split(handler, ".")[0]
      handler = " [by "+handler+"]"
    }
    reply = reply + fmt.Sprintf("== %4v %-9v %v  %v (%v)%v%v", status, job, TimestampRE.ReplaceAllString(answer.Text("timestamp"),"$3.$2 $4:$5:$6"), answer.Text("macaddress"), answer.Text("plainname"),periodic,handler)
  }
  
  return reply
}

const re_1xx = "(1([0-9]?[0-9]?))"
const re_2xx = "(2([6-9]|([0-4][0-9]?)|(5[0-5]?))?)"
const re_xx  = "([3-9][0-9]?)"
const ip_part = "(0|"+re_1xx+"|"+re_2xx+"|"+re_xx+")"
var ipRegexp = regexp.MustCompile("^"+ip_part+"([.]"+ip_part+"){3}$")
var macAddressRegexp = regexp.MustCompile("^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$")

func parseMachine(machine string, template *jobDescriptor) bool {
  var name string
  var ip string
  var mac string
  if strings.Index(machine, "*") >= 0 { return false }
  
  if macAddressRegexp.MatchString(machine) {
    mac = machine
    name = db.SystemPlainnameForMAC(mac)
    if name == "none" { return false }
    ip = db.SystemIPAddressForName(name)
    if ip == "none" { ip = "0.0.0.0" }
  } else if ipRegexp.MatchString(machine) {
    ip = machine
    name = db.SystemNameForIPAddress(ip)
    if name == "none" { return false }
    mac = db.SystemMACForName(name)
    if mac == "none" { return false }
  } else {
    name = machine
    ip = db.SystemIPAddressForName(name)
    if ip == "none" { ip = "0.0.0.0" }
    mac = db.SystemMACForName(name)
    if mac == "none" { return false }
  }
  
  template.MAC = mac
  template.IP = ip
  template.Name = name
  
  return true
}

func parseWild(wild string, template *jobDescriptor) bool {
  if wild == "*" {
    template.MAC = "*"
    template.Name = "*"
    template.IP = "0.0.0.0"
    return true
  }
  return false
}

func parseSubstring(sub string, template *jobDescriptor) bool {
  if sub == "" { return false }
  template.Sub = sub
  return true
}

var dateRegexp = regexp.MustCompile("^20[0-9][0-9]-[0-1][0-9]-[0-3][0-9]$")
var timeRegexp = regexp.MustCompile("^[0-2]?[0-9]:[0-5]?[0-9](:[0-5]?[0-9])?$")
var duraRegexp = regexp.MustCompile("^[0-9]+[smhd]$")

func parseTime(t string, template *jobDescriptor) bool {
  if dateRegexp.MatchString(t) {
    template.Date = strings.Replace(t,"-","",-1)
    return true
  }
  
  if timeRegexp.MatchString(t) {
    parts := strings.Split(t,":")
    t = parts[0]
    if len(t) < 2 { t = "0" + t }
    if len(parts[1]) < 2 { t = t + "0" }
    t += parts[1]
    if len(parts) < 3 { t = t + "00" 
    } else {
      if len(parts[2]) < 2 { t = t + "0" }
      t += parts[2]
    }
    
    template.Time = t
    return true
  }
  
  if duraRegexp.MatchString(t) {
    n,_ := strconv.ParseUint(t[0:len(t)-1], 10, 64)
    var dura time.Duration
    switch t[len(t)-1] {
      case 's': dura = time.Duration(n)*time.Second
      case 'm': dura = time.Duration(n)*time.Minute
      case 'h': dura = time.Duration(n)*time.Hour
      case 'd': dura = time.Duration(n)*24*time.Hour
    }
    
    ts := util.MakeTimestamp(time.Now().Add(dura))
    template.Date = ts[0:8]
    template.Time = ts[8:]
    return true
  }
  
  return false
}

func parseJob(j string, template *jobDescriptor) bool {
  for i := range jobs {
    if strings.HasPrefix(jobs[i],j) {
      template.Job = canonical[i]
      return true
    }
  }

  return false
}


// Parses args and sets config variables accordingly.
func ReadArgs(args []string) {
  // If there is no -l switch, use a non-existent port as default
  // because this will be used as <source> in messages and if we
  // use an existing port and happen to hit the same port as the
  // go-susi we are contacting on localhost, then we get a panic() from
  // Peer() because it looks like we're trying to peer with ourselves.
  config.ServerListenAddress = ":99999"
  
  config.LogLevel = 0
  for i := 0; i < len(args); i++ {
    arg := args[i]
  
    if arg == "-v" || arg == "-vv" || arg == "-vvv" || arg == "-vvvv" || 
       arg == "-vvvvv" || arg == "-vvvvvv" || arg == "-vvvvvvv" {
    
      config.LogLevel = len(arg) - 1
    
    } else if arg == "-c" {
      i++
      if i >= len(args) {
        util.Log(0, "ERROR! ReadArgs: missing argument to -c")
      } else {
        config.ServerConfigPath = args[i]
      }
    } else if arg == "-l" {
      i++
      if i >= len(args) {
        util.Log(0, "ERROR! ReadArgs: missing argument to -l")
      } else {
        config.ServerListenAddress = ":"+args[i]
        ListenForConnections = true
      }
    } else if arg == "-i" {
      Interactive = true
    } else if arg == "-e" {
      i++
      if i >= len(args) {
        util.Log(0, "ERROR! ReadArgs: missing argument to -e")
      } else {
        BatchCommands.Write([]byte("\n"+args[i]))
      }
    } else if arg == "-f" {
      i++
      if i >= len(args) {
        util.Log(0, "ERROR! ReadArgs: missing argument to -f")
      } else {
        f := args[i]
        fi, err := os.Stat(f)
        if err != nil {
          util.Log(0, "ERROR! ReadArgs: Cannot stat \"%v\": %v",f,err)
        } else {
          if fi.IsDir() {
            util.Log(0, "ERROR! ReadArgs: \"%v\" is a directory",f)
          } else {
            if fi.Mode() & os.ModeType == 0 {
              data, err := ioutil.ReadFile(f)
              if err != nil {
                util.Log(0, "ERROR! ReadArgs: Error reading \"%v\": %v",f,err)
              } else {
                BatchCommands.Write([]byte("\n"+string(data)))
              }
            } else {
              SpecialFiles = append(SpecialFiles, f)
            }
          }
        }
      }
    } else if arg == "--help" {
    
      config.PrintHelp = true
      
    } else if arg == "--version" {      
      
      config.PrintVersion = true
    
    } else if arg == "" {
      util.Log(0, "WARNING! ReadArgs: Ignoring empty command line argument")
    } else if arg[0] != '-' {
      TargetAddress = arg
      if strings.Index(TargetAddress, ":") < 0 {
        TargetAddress += ":20081"
      }
    } else {
      util.Log(0, "ERROR! ReadArgs: Unknown command line switch: %v", arg)
    }
  }
}

// unlike config.ReadConfig() this function reads /etc/gosa/gosa.conf
func ReadConfig() {
  conf, _ := xml.FileToHash(config.ServerConfigPath)
  // Ignore parsing errors (such as "stray text outside tag").
  // The result is always valid even if it may be partial data.
  
  conf = conf.First("main")
  if conf == nil {
    util.Log(0, "ERROR! %v: No <main> section", config.ServerConfigPath)
    return
  }
  
  target, err := util.Resolve(TargetAddress, config.IP)
  if err != nil { target = TargetAddress }
  
  found := false
  locs := []string{}
  for loc := conf.First("location"); loc != nil; loc = loc.Next() {
    if x := loc.Text("caCertificate"); x != "" {
      config.CACertPath = strings.Fields(x)
    }
    if x := loc.Text("certificate"); x != "" {
      config.CertPath = x
    }
    if x := loc.Text("keyfile"); x != "" {
      config.CertKeyPath = x
    }
    gosasi := strings.SplitN(loc.Text("gosaSupportURI"), "@", 2)
    key := ""
    server := gosasi[len(gosasi)-1]
    locs = append(locs, server)
    if len(gosasi) > 1 { 
      key = gosasi[0]
      config.ModuleKeys = append(config.ModuleKeys, key)
    }
    server_resolved, err := util.Resolve(server, config.IP)
    if err != nil { server_resolved = server }
    // If this <location> section is the right one for TargetAddress
    if server == target || server_resolved == target ||
       server == TargetAddress || server_resolved == TargetAddress {
      
      found = true
      
      if key != "" {
        config.ModuleKey["[GOsaPackages]"] = key
      }
      
      if ldap := loc.First("referral"); ldap != nil {
        uri := ldap.Text("URI")
        if idx := strings.Index(uri, "="); idx > 0 {
          if idx = strings.LastIndex(uri[0:idx],"/"); idx > 0 {
            config.LDAPURI = uri[0:idx]
            config.LDAPBase = uri[idx+1:]
            config.LDAPAdmin = ldap.Text("adminDn")
            err := ioutil.WriteFile(config.LDAPAdminPasswordFile, []byte(ldap.Text("adminPassword")), 0600)
            if err != nil { util.Log(0, "ERROR! Could not write admin password to file: %v", err) }
            config.LDAPUser = config.LDAPAdmin
            config.LDAPUserPasswordFile = config.LDAPAdminPasswordFile
          }
        }
      }
      
      break
    }
  }
  
  if !found {
    util.Log(0, "ERROR! %v: No <location> section for %v (have: %v)", config.ServerConfigPath, TargetAddress, locs)
  }
  
  config.TLSRequired = len(config.ModuleKey) == 0
}

type TimeoutError struct{}
func (e *TimeoutError) Error() string { return "Timeout" }
func (e *TimeoutError) String() string { return "Timeout" }
func (e *TimeoutError) Temporary() bool { return true }
func (e *TimeoutError) Timeout() bool { return true }

type ReaderWriterConnection struct {
  reader io.Reader
  writer io.Writer
  
  // stores []byte slices and an error if it occurs
  readbuf deque.Deque 
  
  // if an error is read from readbuf it is stored here and returned on every following call
  readerr error 
  rdeadline time.Time
  wdeadline time.Time
}

func (conn* ReaderWriterConnection) bufferFiller() {
  for {
    buf := make([]byte, 4096)
    n, err := conn.reader.Read(buf)
    if n > 0 { conn.readbuf.Push(buf[0:n]) }
    if err != nil { conn.readbuf.Push(err); return; }
  }
}

func (conn *ReaderWriterConnection) Read(b []byte) (n int, err error) {
  if conn.readerr != nil { return 0, conn.readerr }
  if time.Now().Before(conn.rdeadline) {
    dura := conn.rdeadline.Sub(time.Now())
    if dura > 0 {
      if !conn.readbuf.WaitForItem(dura) { 
        return 0,&TimeoutError{} 
      }
    }
  }
  item := conn.readbuf.Next()
  if e,ok := item.(error); ok {
    conn.readerr = e
    return 0, conn.readerr
  }
  slice := item.([]byte)
  if len(slice) <= len(b) {
    return copy(b, slice), nil
  } 
  
  // if len(slice) > len(b)  (i.e. buffer has more data)
  n = copy(b, slice)
  slice = slice[n:]
  conn.readbuf.Insert(slice) // put remaining data back in buffer
  return n, nil
}


func (conn *ReaderWriterConnection) Write(b []byte) (n int, err error) {
  return conn.writer.Write(b)
}

func (conn *ReaderWriterConnection) Close() error {
  var err1 error
  var err2 error
  if closer, ok := conn.reader.(io.Closer); ok {
    err1 = closer.Close()
  }
  if closer, ok := conn.writer.(io.Closer); ok {
    err2 = closer.Close()
  }
  if err1 != nil { return err1 }
  return err2
}

func (conn *ReaderWriterConnection) LocalAddr() net.Addr {
  name1 := fmt.Sprintf("%T",conn.reader)
  name2 := fmt.Sprintf("%T",conn.writer)
  if f,ok := conn.reader.(*os.File); ok {
    name1 = f.Name()
  }
  if f,ok := conn.writer.(*os.File); ok {
    name2 = f.Name()
  }
  return &net.UnixAddr{fmt.Sprintf("%v:%v",name1,name2),"ReaderWriterConnection"}
}

func (conn *ReaderWriterConnection) RemoteAddr() net.Addr { return conn.LocalAddr() }

func (conn *ReaderWriterConnection) SetDeadline(t time.Time) error {
  conn.SetReadDeadline(t)
  conn.SetWriteDeadline(t)
  return nil
}

func (conn *ReaderWriterConnection) SetReadDeadline(t time.Time) error {
  conn.rdeadline = t
  return nil
}

func (conn *ReaderWriterConnection) SetWriteDeadline(t time.Time) error {
  conn.wdeadline = t
  return nil
}

func NewReaderWriterConnection(r io.Reader, w io.Writer) *ReaderWriterConnection {
  conn := &ReaderWriterConnection{reader:r,writer:w}
  go conn.bufferFiller()
  return conn
}

func Dup(fd int, name string) *os.File {
  newfd, err := syscall.Dup(fd)
  if err != nil {
    util.Log(0, "ERROR! %v", err)
    return os.NewFile(uintptr(fd), name)
  }
  return os.NewFile(uintptr(newfd), name)
}
