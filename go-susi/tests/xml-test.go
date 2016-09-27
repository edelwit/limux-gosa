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

// Unit tests run by run-tests.go.
package tests

import (
         "os"
         "fmt"
         "sort"
         "time"
         "bytes"
         "strings"
         "io/ioutil"
         "os/exec"
         
         "../xml"
         "github.com/mbenkmann/golib/util"
       )

// Unit tests for the package susi/xml.
func Xml_test() {
  fmt.Printf("\n=== xml.Hash ===\n\n")
  testHash()
  testSetText()
  testIterator()
  
  fmt.Printf("\n=== xml.HashFilter ===\n\n")
  testFilter()
  
  fmt.Printf("\n=== xml.DB ===\n\n")
  testDB()
}

// "&" starts a new <clause>  (the 1st clause is implicit)
// "!or" or "!and" start a new <phrase>
// everything else is "operator" "comparevalue" pairs
func query(x *xml.Hash, q... string) string {
  qry := xml.NewHash("where")
  clause := qry.Add("clause")
  for i:=0; i < len(q) ; {
    if q[i] == "&" {
      clause = qry.Add("clause")
      i++
    }
    if q[i][0] == '!' {
      clause.Add("connector",q[i][1:])
      i++
    }
    phrase := clause.Add("phrase")
    phrase.Add("operator", q[i])
    phrase.Add("n", q[i+1])
    i += 2
  }
  
  filter, err := xml.WhereFilter(qry)
  if err != nil { return fmt.Sprintf("%v",err) }
  
  result := []string{}
  for item := x.Query(filter).First("item"); item != nil; item = item.Next() {
    result = append(result, item.Text("n"))
  }
  return strings.Join(result, " ")
}

func testFilter() {
  lst := []string{"1","10","10x","2","2x","20","3","30","30x","100x","100"}
  x := xml.NewHash("db")
  for _, dat := range lst {
    x.Add("item").Add("n", dat)
  }
  check(query(x,"eq","10"),"10")
  check(query(x,"!or","eq","2","eq","3"), "2 3")
  check(query(x,"!or","eq","2","eq","3", "&","like","2%" ), "2")
  foo, err := xml.WhereFilter(xml.NewHash("foo"))
  check(foo, nil)
  check(err, "Wrapper element must be 'where', not 'foo'")
  check(query(x,"!foo","eq","2","eq","3"), "Only 'and' and 'or' are allowed as <connector>, not 'foo'")
  y, _ := xml.StringToHash("<where><clause><phrase><foo/><bar/></phrase></clause></where>")
  foo, err = xml.WhereFilter(y)
  check(foo, nil)
  check(err,"<phrase> may only contain one other element besides <operator>")
  y, _ = xml.StringToHash("<where><clause><phrase></phrase></clause></where>")
  foo, err = xml.WhereFilter(y)
  check(foo, nil)
  check(err,"<phrase> must have one other element besides <operator>")
  y, _ = xml.StringToHash("<where></where>")
  foo, err = xml.WhereFilter(y)
  check(err, nil)
  check(x.Query(foo), x)
  check(query(x,"!or","foo","2"), "Unsupported <operator>: foo")
  
  check(xml.FilterAnd([]xml.HashFilter{xml.FilterNone}).Accepts(nil), false)
  check(xml.FilterAnd([]xml.HashFilter{xml.FilterAll}).Accepts(nil), false)
  check(xml.FilterAnd([]xml.HashFilter{}).Accepts(nil), false)
  check(xml.FilterOr([]xml.HashFilter{xml.FilterNone}).Accepts(nil), false)
  check(xml.FilterOr([]xml.HashFilter{xml.FilterAll}).Accepts(nil), false)
  check(xml.FilterOr([]xml.HashFilter{}).Accepts(nil), false)
  check(xml.FilterNone.Accepts(nil), false)
  check(xml.FilterAll.Accepts(nil), false)
  check(xml.FilterNot(xml.FilterAll).Accepts(nil), false)
  check(xml.FilterNot(xml.FilterNone).Accepts(nil), false)
  check(xml.FilterAnd([]xml.HashFilter{}).Accepts(xml.NewHash("foo")), true)
  check(xml.FilterOr([]xml.HashFilter{}).Accepts(xml.NewHash("foo")), false)
  
  filters := []xml.HashFilter{xml.FilterAll}
  filt := xml.FilterAnd(filters)
  check(filt.Accepts(xml.NewHash("foo")), true)
  filters[0] = xml.FilterNone
  check(filt.Accepts(xml.NewHash("foo")), true) // check that changing filters has NOT affected filt
  
  filters = []xml.HashFilter{xml.FilterAll}
  filt = xml.FilterOr(filters)
  check(filt.Accepts(xml.NewHash("foo")), true)
  filters[0] = xml.FilterNone
  check(filt.Accepts(xml.NewHash("foo")), true) // check that changing filters has NOT affected filt
  
  check(xml.FilterRegexp("foo","bar").Accepts(xml.NewHash("foo")), false)
  check(xml.FilterRegexp("foo","").Accepts(xml.NewHash("foo")), false)
  y, _ = xml.StringToHash("<foo><bar>x</bar><bar>y</bar></foo>")
  check(xml.FilterRegexp("bar","a|Y").Accepts(y), false)
  check(xml.FilterRegexp("bar","(?i)a|Y").Accepts(y), true)
  check(xml.FilterRegexp("bar","(?i)a|B").Accepts(y), false)
  check(xml.FilterRegexp("bar","x|y").Accepts(y), true)
  check(xml.FilterNot(xml.FilterRegexp("bar","(?i)a|Y")).Accepts(y), false)
  check(xml.FilterNot(xml.FilterRegexp("bar","(?i)a|B")).Accepts(y), true)
  check(xml.FilterNot(xml.FilterRegexp("bar","x|y")).Accepts(y), false)
  
  check(query(x, "!and", "like", "1%", "unlike", "__X"), "1 10 100x 100")
  check(query(x, "!and", "unlike", "1%", "like", "__X"), "30x")
  check(query(x, "unlike", "."), "1 10 10x 2 2x 20 3 30 30x 100x 100")
  check(query(x, "eq", "."), "")
  check(query(x, "eq", "1"), "1")
  check(query(x, "eq", "x"), "")
  check(query(x, "like", "1"), "1")
  check(query(x, "like", "x"), "")
  
  check(query(x, "ge", "2"), "10 2 2x 20 3 30 30x 100")
  check(query(x, "ge", "2 "), "2x 20 3 30 30x")
  check(query(x, "ge", "2x"), "2x 3 30 30x")
  
  check(query(x, "gt", "2"), "10 2x 20 3 30 30x 100")
  check(query(x, "gt", "2 "), "2x 20 3 30 30x")
  check(query(x, "gt", "2x"), "3 30 30x")
  
  check(query(x, "lt", "2"), "1 10x 100x")
  check(query(x, "lt", "2 "), "1 10 10x 2 100x 100")
  check(query(x, "lt", "2x"), "1 10 10x 2 20 100x 100")
  
  check(query(x, "le", "2"), "1 10x 2 100x")
  check(query(x, "le", "2 "), "1 10 10x 2 100x 100")
  check(query(x, "le", "2x"), "1 10 10x 2 2x 20 100x 100")
  
  y, _ = xml.StringToHash("<d><i><x> </x></i><i><x>foo</x></i><i><x>bar</x><y>bla</y><x>foo</x></i></d>")
  check(y.Query(xml.FilterSimple()), y)
  check(y.Query(xml.FilterSimple("foo")), y)
  check(y.Query(xml.FilterSimple("foo","")), "<d></d>")
  check(y.Query(xml.FilterSimple("x","")), "<d></d>")
  check(y.Query(xml.FilterSimple("x"," ")), "<d><i><x> </x></i></d>")
  check(y.Query(xml.FilterSimple("x","FoO")), "<d><i><x>foo</x></i><i><x>bar</x><x>foo</x><y>bla</y></i></d>")
  check(y.Query(xml.FilterSimple("x","foo", "y")), "<d><i><x>foo</x></i><i><x>bar</x><x>foo</x><y>bla</y></i></d>")
  check(y.Query(xml.FilterSimple("x","foo", "y", "BLA")), "<d><i><x>bar</x><x>foo</x><y>bla</y></i></d>")
  check(xml.FilterSimple("x","foo", "y", "bla").Accepts(nil), false)
  check(xml.FilterSimple().Accepts(nil), false)
  check(xml.FilterSimple("foo").Accepts(nil), false)
  
  lst = []string{"1","10","10X","2","2x","20","3","30","30x","100X","100"}
  x = xml.NewHash("db")
  for _, dat := range lst {
    x.Add("item").Add("n", dat)
  }
  
  check(query(x, "!and", "like", "1%", "unlike", "__X"), "1 10 100X 100")
  check(query(x, "!and", "unlike", "1%", "like", "__X"), "30x")
  check(query(x, "unlike", "."), "1 10 10X 2 2x 20 3 30 30x 100X 100")
  check(query(x, "eq", "."), "")
  check(query(x, "eq", "1"), "1")
  check(query(x, "eq", "x"), "")
  check(query(x, "eq", "100x"), "100X")
  check(query(x, "eq", "2X"), "2x")
  check(query(x, "eq", "2x"), "2x")
  check(query(x, "ne", "2X"), "1 10 10X 2 20 3 30 30x 100X 100")
  check(query(x, "like", "1"), "1")
  check(query(x, "like", "x"), "")
  
  check(query(x, "ge", "2"), "10 2 2x 20 3 30 30x 100")
  check(query(x, "ge", "2 "), "2x 20 3 30 30x")
  check(query(x, "ge", "2X"), "2x 3 30 30x")
  check(query(x, "ge", "30x"), "30x")
  check(query(x, "ge", "30X"), "30x")
  
  check(query(x, "gt", "2"), "10 2x 20 3 30 30x 100")
  check(query(x, "gt", "2 "), "2x 20 3 30 30x")
  check(query(x, "gt", "2x"), "3 30 30x")
  
  check(query(x, "lt", "2"), "1 10X 100X")
  check(query(x, "lt", "2 "), "1 10 10X 2 100X 100")
  check(query(x, "lt", "2X"), "1 10 10X 2 20 100X 100")
  
  check(query(x, "le", "2"), "1 10X 2 100X")
  check(query(x, "le", "2 "), "1 10 10X 2 100X 100")
  check(query(x, "le", "2x"), "1 10 10X 2 2x 20 100X 100")
  
}


type StringChannel chan string
type ChannelStorer struct {
  StringChannel
}

func (self *ChannelStorer) Store(db *xml.Hash) error {
  self.StringChannel <- db.String()
  return nil
}

type FilterAllButC struct{}
func (*FilterAllButC) Accepts(x *xml.Hash) bool {
  if x == nil || x.Text() == "c" { return false }
  return true
}

func testDB() {
  tempfile, _ := ioutil.TempFile("", "xml-test-")
  tempname := tempfile.Name()
  tempfile.Close()
  fstor := xml.FileStorer{tempname}
  teststring := "This is a test!\nLine 2"
  check(fstor.Store(xml.NewHash("foo",teststring)), nil)
  checkdata, err := ioutil.ReadFile(tempname)
  check(err, nil)
  check(string(checkdata), "<foo>"+teststring+"</foo>")
  os.Remove(tempname)
  
  db := xml.NewDB("fruits", nil, 0)
  banana := xml.NewHash("fruit", "banana")
  db.AddClone(banana)
  banana.SetText("yellow banana") // must not affect database entry
  check(db.Query(xml.FilterAll), "<fruits><fruit>banana</fruit></fruits>")
  check(db.Query(xml.FilterNone), "<fruits></fruits>")
  
  delay := time.Duration(2*time.Second)
  cstore := &ChannelStorer{make(chan string,1)}
  persistdb := xml.NewDB("pairs", cstore, delay)
  start := time.Now()
  letters := "abcdefghijklmnopqrstuvwxyz"
  checkstr := "<pairs>"
  for i:= 0; i < 26; i++ {
    for j:= 0; j < 26 ; j++ {
      tag := string(letters[i])+string(letters[j])
      checkstr = checkstr + "<" + tag + "></" + tag + ">"
      go persistdb.AddClone(xml.NewHash(tag))
    }
  }
  s := "Timeout waiting for persist job"
  select {
    case s = <- cstore.StringChannel : // s received
    case <-time.After(delay + 500 * time.Millisecond) : // timeout
  }
  if time.Since(start) < delay - 500 * time.Millisecond {
    s = "Persist job started too soon"
  }
  checkstr = checkstr + "</pairs>"
  check(s, checkstr)
  
  s = ""
  select {
    case <- cstore.StringChannel : s = "Spurious persist job detected"
    case <-time.After(delay + 500 * time.Millisecond) : // timeout
  }
  check(s, "")
  
  start = time.Now()
  all := persistdb.Remove(xml.FilterAll)
  check(all, checkstr)
  s = "Timeout waiting for persist job"
  select {
    case s = <- cstore.StringChannel : // s received
    case <-time.After(delay + 500 * time.Millisecond) : // timeout
  }
  if time.Since(start) < delay - 500 * time.Millisecond {
    s = "Persist job started too soon"
  }
  check(s, "<pairs></pairs>")
  
  persistdb.Init(all) // Init does NOT create a persist job!
  check(persistdb.Query(xml.FilterAll), checkstr)
  s = ""
  select {
    case <- cstore.StringChannel : s = "Spurious persist job detected"
    case <-time.After(delay + 500 * time.Millisecond) : // timeout
  }
  check(s, "")
  
  start = time.Now()
  persistdb.Shutdown()
  s = "Shutdown failed to persist the database"
  select {
    case s = <- cstore.StringChannel : // s received
    default: // persisting must already have happened when Shutdown() returns
  }
  check(s, checkstr)
  
  s = ""
  go func(){
    persistdb.Query(xml.FilterNone) // must block forever
    s = "Shutdown did not lock the database"
  }()
  time.Sleep(1*time.Second)
  check(s,"")
  
  
  x,_ := xml.StringToHash("<letters><let>a</let><let>b</let><let>c</let><let>d</let><let>c</let><let>e</let></letters>")
  db.Init(x)
  check(db.Remove(&FilterAllButC{}), "<letters><let>a</let><let>b</let><let>d</let><let>e</let></letters>")
  check(db.Query(xml.FilterAll), "<letters><let>c</let><let>c</let></letters>")
  check(db.Replace(xml.FilterNone, true, xml.NewHash("let","d")),"<letters></letters>")
  check(db.Query(xml.FilterAll), "<letters><let>c</let><let>c</let></letters>")
  check(db.Replace(xml.FilterNone, false, xml.NewHash("let","d")), "<letters></letters>")
  check(db.Query(xml.FilterAll), "<letters><let>c</let><let>c</let><let>d</let></letters>")
  check(db.Replace(&FilterAllButC{}, true, xml.NewHash("let","e"), xml.NewHash("let","f")), "<letters><let>d</let></letters>")
  check(db.Query(xml.FilterAll), "<letters><let>c</let><let>c</let><let>e</let><let>f</let></letters>")
  
  x = xml.NewHash("fruits")
  color := []string{"yellow", "green", "orange", "red"}
  for i, f := range []string{"banana", "apple", "peach", "cherry"} {
    fruit := x.Add("fruit")
    fruit.Add("name", f)
    fruit.Add("color", color[i])
  }
  x.Add("vehicle").Add("name", "car")
  db.Init(x)
  names := db.ColumnValues("name")
  sort.Strings(names)
  check(names, []string{"apple","banana", "car", "cherry", "peach"})
  check(db.ColumnValues("color"), []string{"yellow", "green", "orange", "red"})
  
  db.Init(x)
}

type filterafter struct { prev *xml.Hash }
func (f *filterafter) Accepts(item *xml.Hash) bool {
  if item == nil { return false }
  if item == f.prev { f.prev = nil; return false }
  if f.prev == nil { f.prev = item; return true }
  return false
}

func testHash() { 
  f := xml.NewHash("fruit", "banana")
  check(f.Verify(),nil)
  check(f, "<fruit>banana</fruit>")
  
  f = xml.NewHash("fruit", "banana","")
  check(f.Verify(),nil)
  check(f, "<fruit><banana></banana></fruit>")
  
  f = xml.NewHash("fruit", "yellow","banana")
  check(f.Verify(),nil)
  check(f, "<fruit><yellow>banana</yellow></fruit>")
  
  f = xml.NewHash("fruit", "yellow","long","banana")
  check(f.Verify(),nil)
  check(f, "<fruit><yellow><long>banana</long></yellow></fruit>")
  
  f = xml.NewHash("fruit", "yellow","long","banana")
  yellow := f.First("yellow")
  f.First("yellow").Rename("green")
  check(f.Verify(),nil)
  check(f, "<fruit><green><long>banana</long></green></fruit>")
  check(yellow.Name(),"green")
  
  x := xml.NewHash("foo")
  check(f.Verify(),nil)
  check(x, "<foo></foo>")
  
  x.SetText(">Dıes ist ein >>>Test<<<<")
  check(x.String(), "<foo>&gt;Dıes ist ein &gt;&gt;&gt;Test&lt;&lt;&lt;&lt;</foo>")
  check(x.Text(), ">Dıes ist ein >>>Test<<<<")
  foo_haver := xml.NewHash("foo_haver")
  foo_haver.AddClone(x)
  check(foo_haver.Verify(),nil)
  check(x.Verify(),nil)
  check(foo_haver.Get("foo"),[]string{">Dıes ist ein >>>Test<<<<"})
  check(foo_haver.Text("foo"),">Dıes ist ein >>>Test<<<<")
  check(foo_haver.Text(),"")
  check(foo_haver.String(), "<foo_haver><foo>&gt;Dıes ist ein &gt;&gt;&gt;Test&lt;&lt;&lt;&lt;</foo></foo_haver>")
  
  x.SetText("Dies ist %v %vter Test","ein",2)
  check(x, "<foo>Dies ist ein 2ter Test</foo>")
  
  bar := x.Add("bar")
  check(bar, "<bar></bar>")
  
  bar.Add("server", "srv1")
  check(x, "<foo>Dies ist ein 2ter Test<bar><server>srv1</server></bar></foo>")
  
  srv4 := bar.Add("server", "srv2")
  for _, s := range []string{"srv3", "srv4"} {
    srv4 = bar.Add("server", s)
  }
  check(x, "<foo>Dies ist ein 2ter Test<bar><server>srv1</server><server>srv2</server><server>srv3</server><server>srv4</server></bar></foo>")
  
  srv4.Add("alias", "foxtrott")
  srv4.Add("alias", "alpha")
  check(x, "<foo>Dies ist ein 2ter Test<bar><server>srv1</server><server>srv2</server><server>srv3</server><server>srv4<alias>foxtrott</alias><alias>alpha</alias></server></bar></foo>")
  
  x_clone := x.Clone()
  x_str := x.String()
  
  srv4.Add("alias", "bravo")
  check(x, "<foo>Dies ist ein 2ter Test<bar><server>srv1</server><server>srv2</server><server>srv3</server><server>srv4<alias>foxtrott</alias><alias>alpha</alias><alias>bravo</alias></server></bar></foo>")
  
  srv4.Add("alias", "delta")
  check(x, "<foo>Dies ist ein 2ter Test<bar><server>srv1</server><server>srv2</server><server>srv3</server><server>srv4<alias>foxtrott</alias><alias>alpha</alias><alias>bravo</alias><alias>delta</alias></server></bar></foo>")
  
  check(hash("a(b(c(d(x)))b(y)z)"),"<a>z<b><c><d>x</d></c></b><b>y</b></a>")
  
  lst := x.Get("foo") // x is a <foo> but contains no <foo> !!
  check(lst, []string{})
  
  lst = x.Get()
  check(lst, []string{})
  
  lst = x.Get("bar")
  check(lst, []string{bar.Text()})
  
  lst = bar.Get("server")
  check(lst, []string{"srv1","srv2","srv3",srv4.Text()})
  
  txt := srv4.Text("alias")
  check(txt, "foxtrott\u241ealpha\u241ebravo\u241edelta")
  
  txt = srv4.Text("alias","doesnotexist")
  check(txt, "foxtrott\u241ealpha\u241ebravo\u241edelta")
  
  x.Add("max", "X")
  x.Add("moritz", "X")
  txt = x.Text("max", "moritz")
  check(txt, "X\u241eX")
  
  y, xmlerr := xml.StringToHash("<foo/><bar/>")
  check(y, "<bar></bar>")  // last one wins
  check(xmlerr, "StringToHash(): Multiple top-level elements")
  
  y, xmlerr = xml.StringToHash("Nix gut")
  check(y, "<xml></xml>")  // dummy returned if nothing properly parsed
  check(xmlerr, "StringToHash(): Stray text outside of tag: Nix gut")
  
  y, xmlerr = xml.StringToHash("<?xml version=\"1.0\"?><foo>We<bar>Hallo</bar>lt</foo>")
  check(y, "<foo>Welt<bar>Hallo</bar></foo>")
  check(xmlerr, "StringToHash(): Unsupported XML token: {xml [118 101 114 115 105 111 110 61 34 49 46 48 34]}")
  
  _, xmlerr = xml.StringToHash("<-foo></-foo>")
  check(xmlerr, "StringToHash(): XML syntax error on line 1: invalid XML name: -foo")
  
  y, xmlerr = xml.StringToHash("<foo bar=\"cool\"><drink more='yes' alcohol='sure'><sauf/></drink></foo>")
  check(y,"<foo><bar>cool</bar><drink><alcohol>sure</alcohol><more>yes</more><sauf></sauf></drink></foo>")
  check(xmlerr, nil)
  
  x_str2 := x.String()
  x_clone2 := x.AddClone(x)
  x_str3 := x.String()
  x_clone3 := x.AddClone(x)
  check(x_clone2, x_str2)
  check(x_clone3, x_str3)
  check(x.First("foo"), x_clone2)
  check(x.First("foo").Next(), x_clone3)
  check(x.First("foo").Next().First("foo"), x_clone2)
  
  check(x_clone, x_str)
  check(x_clone.Verify(), nil)
  check(x.Verify(), nil)
  check(x_clone2.Verify(), nil)
  check(x_clone3.Verify(), nil)
  
  broken := xml.NewHash("b0rken")
  var panic_err interface{}
  func() {
    defer func() {
      panic_err = recover()
    }()
    broken.AddWithOwnership(broken)
  }()
  check(panic_err, "AddWithOwnership: Sanity check failed!")
  
  panic_err = nil
  func() {
    defer func() {
      panic_err = recover()
    }()
    broken.AddWithOwnership(nil)
  }()
  check(panic_err, "AddWithOwnership: Sanity check failed!")
  
  panic_err = nil
  twin := broken.Add("twin")
  func() {
    defer func() {
      panic_err = recover()
    }()
    broken.AddWithOwnership(twin)
  }()
  check(panic_err, "AddWithOwnership: Sanity check failed!")
  
  panic_err = nil
  func() {
    defer func() {
      panic_err = recover()
    }()
    x.AddWithOwnership(x.First("foo"))
  }()
  check(panic_err, "AddWithOwnership: Sanity check failed!")
  
  family := xml.NewHash("family")
  mother := xml.NewHash("mother")
  mother.Add("abbr","mom")
  father := xml.NewHash("father")
  father.Add("abbr", "dad")
  father.Add("abbr", "daddy")
  family.AddWithOwnership(mother)
  check(family.First("mother") == mother, true)
  family.AddWithOwnership(father)
  check(family.First("father") == father, true)
  mommy := xml.NewHash("abbr")
  mommy.SetText("mommy")
  mother.AddWithOwnership(mommy)
  mum := xml.NewHash("abbr")
  mum.SetText("mum")
  mother.AddWithOwnership(mum)
  mummy := xml.NewHash("abbr")
  mummy.SetText("mummy")
  mother.AddWithOwnership(mummy)
  check(mother.First("abbr").Next() == mommy, true)
  check(mommy.Next() == mum, true)
  check(mum.Next() == mummy, true) 
  
  temp_mother := family.RemoveFirst("mother")
  check(temp_mother.Verify(),nil)
  family.AddWithOwnership(temp_mother)
  check(mother.RemoveFirst("not_present"), nil)
  mom := mother.First("abbr")
  mother.AddWithOwnership(mother.RemoveFirst("abbr"))
  check(mother.First("abbr") == mommy, true)
  check(mummy.Next() == mom, true)
  father.RemoveFirst("abbr")
  check(father.Text("abbr"), "daddy")
  check(mother.Get("abbr"), []string{"mommy","mum","mummy","mom"})
  
  check(family.Verify(), nil)
  
  ducks    := xml.NewHash("ducks")
  dewey    := ducks.Add("duck", "dewey")
  donald   := ducks.Add("duck", "donald")
  first_removed_duck := ducks.RemoveFirst("duck")
  check(first_removed_duck == dewey, true)
  check(dewey.Verify(), nil)
  daisy    := ducks.Add("duck", "daisy")
  darkwing := ducks.Add("duck", "darkwing")
  check(ducks.First("duck") == donald, true)
  check(donald.Next() == daisy, true)
  check(daisy.Next() == darkwing, true)
  check(darkwing.Next() == nil, true)
  
  
  check(ducks.Remove(&filterafter{darkwing}), "<ducks></ducks>")
  check(ducks.Remove(&filterafter{donald}).RemoveFirst("duck") == daisy, true)
  check(daisy.Verify(), nil)
  check(ducks.Verify(), nil)
  check(daisy.Next(), nil)
  check(ducks, "<ducks><duck>donald</duck><duck>darkwing</duck></ducks>")
  ducks.AddWithOwnership(daisy)
  check(ducks.Remove(&filterafter{darkwing}).First("duck") == daisy, true)
  check(daisy.Verify(), nil)
  check(daisy.Next(), nil)
  check(ducks.Verify(), nil)
  check(ducks.Remove(&filterafter{donald}).First("duck") == darkwing, true)
  check(darkwing.Verify(), nil)
  check(darkwing.Next(), nil)
  check(ducks.Verify(), nil)
  check(ducks, "<ducks><duck>donald</duck></ducks>")
  
  for num_kids := 1; num_kids < 5; num_kids++ {
    kids := xml.NewHash("kids")
    for i := 0; i < num_kids; i++ {
      kids.Add("kid", fmt.Sprintf("%d",i+1))
    }
    test_kids := kids.Clone()
    check(test_kids.Verify(),nil)
    test_kid := test_kids.RemoveFirst("kid")
    if !check(test_kid.Verify(), nil) { fmt.Printf("FAILED for num_kids=%v\n",num_kids) }
    for i := 1; i < num_kids; i++ {
       test_kids = kids.Clone()
       test_kid = test_kids.First("kid")
       for j := 0; j < i; j++ { test_kid = test_kid.Next() }
       test_kid = test_kids.Remove(&filterafter{test_kid}).First("kid")
       check(test_kids.Verify(),nil)
       if i+1 == num_kids { 
         check(test_kid, nil) 
       } else {
         if !check(test_kid.Verify(), nil) { fmt.Printf("FAILED for num_kids=%v i=%v\n",num_kids,i) }
       }
    }
  }
  
  xyzzy := xml.NewHash("x")
  xyzzy.Add("y","a")
  xyzzy.Add("y","b")
  xyzzy.Add("y","c")
  xyzzy.First("y").Add("A")
  xyzzy.First("y").Add("B")
  xyzzy.First("y").Add("C")
  xyzzy.First("y").Add("D")
  subtags := xyzzy.First("y").Subtags()
  sort.Strings(subtags)
  check(subtags, []string{"A","B","C","D"})
  
  xmlreader := bytes.NewBufferString("<xml>\n<foo>\nbar</foo>\n</xml>\x00Should be ignored\n")
  x, xmlerr = xml.ReaderToHash(xmlreader)
  check(xmlerr, "StringToHash(): XML syntax error on line 4: illegal character code U+0000")
  
  xmlreader = bytes.NewBufferString("<xml>\n<foo>\nbar</foo>\n</xml>")
  x, xmlerr = xml.ReaderToHash(xmlreader)
  check(xmlerr, nil)
  check(x,"<xml>\n\n<foo>\nbar</foo></xml>")
  
  x = xml.NewHash("xml")
  letters := "eacdb"
  for i := range letters {
    child := x.Add(letters[i:i+1])
    for i := range letters {
      child.Add(letters[i:i+1])
    }
  }
  check(x.Verify(), nil)
  check(x.String(),"<xml><a><a></a><b></b><c></c><d></d><e></e></a><b><a></a><b></b><c></c><d></d><e></e></b><c><a></a><b></b><c></c><d></d><e></e></c><d><a></a><b></b><c></c><d></d><e></e></d><e><a></a><b></b><c></c><d></d><e></e></e></xml>")
  
  x = xml.NewHash("foo")
  check(x.FirstOrAdd("id"),"<id></id>")
  x.FirstOrAdd("id").SetText("1")
  check(x.FirstOrAdd("id"),"<id>1</id>")
  check(x.FirstOrAdd("bar"),"<bar></bar>")
  x.Rename("foobar")
  check(x,"<foobar><bar></bar><id>1</id></foobar>")
  
  godzilla := xml.NewHash("monster","godzilla")
  godzilla.Add("home","japan")
  godzilla.AppendString(" junior")
  monsters := xml.NewHash("monsters")
  monsters.AddWithOwnership(godzilla)
  monsters.Add("monster","gamera")
  godclone := godzilla.Clone()
  check(godclone.Verify(), nil)
  check(godzilla.Next(),"<monster>gamera</monster>")
  check(godclone.Next(),nil)
  check(godclone,"<monster>godzilla junior<home>japan</home></monster>")
  monsters.SetText("are cool")
  check(monsters,"<monsters>are cool<monster>godzilla junior<home>japan</home></monster><monster>gamera</monster></monsters>")
  monsters.First("monster").SetText("mecha-%v",monsters.Get("monster")[0])
  check(monsters,"<monsters>are cool<monster>mecha-godzilla junior<home>japan</home></monster><monster>gamera</monster></monsters>")
  check(monsters.Verify(), nil)
  check(monsters.RemoveFirst("godzilla"), nil)
  check(monsters.Verify(), nil)
  check(monsters,"<monsters>are cool<monster>mecha-godzilla junior<home>japan</home></monster><monster>gamera</monster></monsters>")
  monsters.FirstOrAdd("creature").SetText("gollum")
  check(monsters.First("creature"), "<creature>gollum</creature>")
  check(godzilla.Next().Next(), nil)
  check(monsters.FirstChild().Element() == godzilla, true)
  check(monsters.FirstChild().Next().Element() == godzilla.Next(), true)
  st := monsters.Subtags()
  sort.Strings(st)
  check(st, []string{"creature","monster"})
  check(len(st),2)
  
  x = xml.NewHash("foo")
  whole := []byte{}
  for n := 0; n <= 12; n++ {
    buf := make([]byte, n)
    for i := range buf { buf[i] = byte(i) }
    x.AppendString(string(buf))
    whole = append(whole, buf...)
  }
  x.EncodeBase64()
  encoded := string(util.Base64EncodeString(string(whole)))
  check(x.Text(), encoded)
  x.DecodeBase64()
  check([]byte(x.Text()),whole)
  x.SetText()
  for i := range encoded {
    x.AppendString(string([]byte{encoded[i]}))
  }
  x.DecodeBase64()
  check([]byte(x.Text()),whole)
  
  x.SetText("H")
  x.EncodeBase64()
  check(x.Text(),"SA==")
  x.DecodeBase64()
  x.AppendString("a")
  x.EncodeBase64()
  check(x.Text(),"SGE=")
  x.DecodeBase64()
  x.AppendString("l")
  x.EncodeBase64()
  check(x.Text(),"SGFs")
  
  x.SetText("H")
  x.AppendString("a")
  x.EncodeBase64()
  check(x.Text(),"SGE=")
  
  x.SetText("H")
  x.AppendString("a")
  x.AppendString("l")
  x.EncodeBase64()
  check(x.Text(),"SGFs")
  
  x.SetText("H")
  x.AppendString("a")
  x.AppendString("l")
  x.AppendString("l")
  x.EncodeBase64()
  check(x.Text(),"SGFsbA==")
  
  x.SetText("H")
  x.AppendString("a")
  x.AppendString("l")
  x.AppendString("l")
  x.AppendString("o")
  x.EncodeBase64()
  check(x.Text(),"SGFsbG8=")
  
  x.SetText("Ha")
  x.AppendString("ll")
  x.AppendString("o")
  x.EncodeBase64()
  check(x.Text(),"SGFsbG8=")
  
  x.SetText("S")
  x.AppendString("G")
  x.AppendString("Fs")
  x.AppendString("b")
  x.AppendString("G8")
  x.DecodeBase64()
  check(x.Text(),"Hallo")
  
  testLDIF()
}

func testSetText() {
  x := xml.NewHash("foo")
  
  x.SetText("%v%%")
  check(x, "<foo>%v%%</foo>")
  
  x.SetText()
  check(x, "<foo></foo>")
  
  x.SetText([]byte{'H','<','l','l','o','&'})
  check(x, "<foo>H&lt;llo&amp;</foo>")
  
  x.SetText([]byte{})
  check(x, "<foo></foo>")
  
  x.SetText([][]byte{[]byte{},[]byte("Hi"),[]byte(" there!")})
  check(x, "<foo>Hi there!</foo>")
  
  x.SetText([]string{"","Hi"," there!"})
  check(x, "<foo>Hi there!</foo>")
  
  x.SetText([][]byte{})
  check(x, "<foo></foo>")
  
  x.SetText([]string{})
  check(x, "<foo></foo>")
  
  x.SetText(10)
  check(x, "<foo>10</foo>")
  
  x.SetText([]bool{true,false})
  check(x, "<foo>[true false]</foo>")
  
  x.SetText("This is a number: %d", 999)
  check(x, "<foo>This is a number: 999</foo>")
  
  x.SetText("%v", []bool{true,false})
  check(x, "<foo>[true false]</foo>")
  
  x.SetText("%v%%","")
  check(x, "<foo>%</foo>")
  
  x.SetText("%v%v%v%s%s","","","","","")
  check(x, "<foo></foo>")
}

func testIterator() {
  check(xml.NewHash("foo").FirstChild() == nil, true)
  check(xml.NewHash("foo","bar").FirstChild() == nil, true)

  x := hash("x(a(e(bla))a(gurker)b(f(bla))c(bla))")
  iter := x.FirstChild()
  check(iter.Element(), hash("a(e(bla))"))
  next := iter.Next()
  check(iter.Element(), hash("a(e(bla))"))
  check(next.Element(), iter.Element().Next())
  check(next.Element(), hash("a(gurker)"))
  next = next.Next()
  check(next.Element(), hash("b(f(bla))"))
  next = next.Next()
  check(next.Element(), hash("c(bla)"))
  check(next.Next() == nil, true)
  
  iter = x.FirstChild()
  check(iter.Remove(), hash("a(e(bla))"))
  check(iter.Element() == nil, true)
  next = iter.Next()
  check(next.Remove(), hash("a(gurker)"))
  check(next.Element()==nil, true)
  next = next.Next()
  check(next.Remove(), hash("b(f(bla))"))
  check(next.Remove()==nil, true)
  check(next.Element()==nil, true)
  next = next.Next()
  check(next.Remove(), hash("c(bla)"))
  check(next.Element()==nil, true)
  check(next.Next() == nil, true)
  
}

type brokenreader bool

func (self brokenreader) Read(p []byte) (n int, err error) { return 0, fmt.Errorf("broken") }

func testLDIF() {
  var broken brokenreader
  x, err := xml.LdifToHash("foo", true, broken)
  check(x,xml.NewHash("xml"))
  check(err,"broken")
  
  x, err = xml.LdifToHash("foo", true, 10)
  check(x,xml.NewHash("xml"))
  check(err,"ldif argument has unsupported type")
  
  x, err = xml.LdifToHash("foo", true, exec.Command("sdjhsadfhb3h4bw3er7zsdbfsdjhbfsdhbf"))
  check(x,xml.NewHash("xml"))
  check(err != nil, true)
  
  x, err = xml.LdifToHash("foo", true, exec.Command("cat", "/sdjhsadfhb3h4bw3er7zsdbfsdjhbfsdhbf"))
  check(x,xml.NewHash("xml"))
  check(err != nil, true)
  
  x, err = xml.LdifToHash("", false, exec.Command("cat", "testdata/nova.ldif"))
  check(err, nil)
  check(x.Text("cn"), "nova")
  check(x.Text("dn"), "cn=nova,ou=workstations,ou=systems,ou=Direktorium,o=Landeshauptstadt München,c=de")
  check(x.Get("objectClass"),[]string{"GOhard", "top", "gotoWorkstation", "FAIobject", "gosaAdministrativeUnitTag"})
  check(x.Text("FAIstate"), "localboot")
  tags := x.Subtags()
  sort.Strings(tags)
  check(tags, []string{"FAIstate", "cn","dn","objectClass"})
  
  nothingfile,_ := os.Open("testdata/nothing.ldif")
  x, err = xml.LdifToHash("", true, nothingfile)
  check(err,nil)
  check(x,xml.NewHash("xml"))
  
  x, err = xml.LdifToHash("dev", true, exec.Command("cat", "testdata/dev.ldif"))
  check(err,nil)
  check(x.Subtags(),[]string{"dev"})
  count := 0
  for d := x.First("dev"); d != nil; d=d.Next() {
    count++
    check(d.Text("dn"), fmt.Sprintf("cn=%v,ou=workstations,ou=systems,ou=Direktorium,o=Landeshauptstadt München,c=de",d.Text("cn")))
    check(d.Get("objectclass"),[]string{"GOhard", "top", "gotoWorkstation", "FAIobject", "gosaAdministrativeUnitTag"})
    check(d.Text("faistate"), "localboot")
    check(checkTags(d,"cn,dn,faistate,objectclass+,broken1?,broken2?,broken3?,broken4?"),"")
  }
  check(count, 4)
  check(x.First("dev").First("broken1") != nil,true)
  check(x.First("dev").Text("broken1"),"")
  check(x.First("dev").First("broken4") != nil,true)
  check(x.First("dev").Text("broken4"),"")
  check(x.First("dev").Text("broken2"),"B0rken2")
  check(x.First("dev").Text("broken3"),"b0rken")
  
  einfo1 := []*xml.ElementInfo{&xml.ElementInfo{"BIG","small", true},
                               &xml.ElementInfo{"big","bigger", false},
                               &xml.ElementInfo{"big","biggest", false},
                               &xml.ElementInfo{"Boom","small", true},
                               }
  x, err = xml.LdifToHash("", true, exec.Command("cat", "testdata/eleminfo.ldif"), einfo1...)
  check(err,nil)
  check(checkTags(x,"bigger+"),"")
  check(x.Text(),"")
  check(x.Get("bigger"), []string{"Buck Bunny", "Bad Wolf"})
  
  x, err = xml.LdifToHash("", false, exec.Command("cat", "testdata/eleminfo.ldif"), einfo1...)
  check(err,nil)
  check(checkTags(x,"small+"),"")
  check(x.Text(),"")
  check(x.Get("small"), []string{"QnVjayBCdW5ueQ==","QmFzdGlj"})
  
  x, err = xml.LdifToHash("item", false, exec.Command("cat", "testdata/eleminfo.ldif"), &xml.ElementInfo{"giga","gugu",true})
  check(err,nil)
  check(x,"<xml></xml>")
  
  x, err = xml.LdifToHash("item", false, exec.Command("cat", "testdata/eleminfo.ldif"), &xml.ElementInfo{"Boom","Boom",false})
  check(err,nil)
  check(x,"<xml><item><Boom>Bastic</Boom></item></xml>")
}
