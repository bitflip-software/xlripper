package xlripper

import (
	"bytes"
	"fmt"
	"io"
	"sync"
)

var badPair = indexPair{-1, -1}
var badTagLoc = tagLoc{badPair, badPair}

const (
	lChevron = '<'
	rChevron = '>'
)

func shparse(zs zstruct, sheetIndex int) (Sheet, error) {
	sh := NewSheet()

	if sheetIndex < 0 || sheetIndex >= len(zs.info.sheetMeta) {
		return sh, fmt.Errorf("bad sheet index '%d', the zstruct has only '%d' sheets", sheetIndex, len(zs.info.sheetMeta))
	}

	meta := zs.info.sheetMeta[sheetIndex]
	sh.Name = meta.sheetName
	sh.Index = sheetIndex
	data, err := shload(meta)

	if err != nil {
		return sh, err
	}

	next := 0
	ch := make(rowChan, rowRoutines)
	loopIX := 0
	sendWait := sync.WaitGroup{}
	receiveWait := sync.WaitGroup{}
	receiveWait.Add(1)
	go receiveRowsAsync(ch, &sh, &receiveWait)

rowLoop:
	for {
		openLoc, _ := shFindFirstOccurenceOfElement(data, next, len(data), "row")

		if openLoc == badPair {
			break rowLoop
		}

		closeLoc, _ := shTagCloseFind(data, openLoc.last+1, len(data), "row")

		if closeLoc == badPair {
			break rowLoop
		}

		//if closeLoc.first == closeLoc.last {
		//	// self closing tag
		//	closeLoc = openLoc
		//}

		rowLoc := tagLoc{openLoc, closeLoc}
		r := rowInfo{}
		r.rowLoc = rowLoc
		r.top.runes = data
		r.top.shared = zs.info.sharedStrings
		r.interationIX = loopIX

		sendWait.Add(1)
		go parseRowAsync(r, ch, &sendWait)
		next = rowLoc.close.last + 1
	}

	sendWait.Wait()
	close(ch)
	receiveWait.Wait()
	return sh, nil
}

// shload reads the worksheet file and returns the unzipped data therein as a slice of runes
func shload(meta sheetMeta) ([]rune, error) {
	if meta.file == nil {
		return make([]rune, 0), fmt.Errorf("the file is nil")
	}

	reader, err := meta.file.Open()

	if err != nil {
		return make([]rune, 0), err
	}

	defer reader.Close()
	buf := bytes.Buffer{}
	io.Copy(&buf, reader)
	data := string(buf.Bytes())
	return []rune(data), nil
}

// shadvance starts at 'first' and advances until it finds 'r' then returns the index of 'r'. returns -1 if 'r' is not
// found
func shadvance(runes []rune, start int, r rune) int {
	e := len(runes)
	ix := start

	if start < 0 {
		return -1
	}

	for ; ix < e; ix++ {
		if runes[ix] == r {
			return ix
		}
	}

	return -1
}

// shFindFirstOccurenceOfElement does stuff
func shFindFirstOccurenceOfElement(runes []rune, first, last int, elem string) (location indexPair, isSelfClosing bool) {
	ix := shSetFirst(runes, first)
	e := shSetLast(runes, last)

	for ; ix <= e && runes[ix] != lChevron; ix++ {
		// advance to an lCheveron
	}

	lChevPos := ix

	if ix > e || runes[ix] != lChevron {
		return badPair, false
	}

	open := badPair

	if runes[ix] != '/' {
		open, ix, isSelfClosing = shTagOpenFind(runes, lChevPos, e, elem)

		if open != badPair {
			return open, isSelfClosing
		}
	}

	for ; ix <= e && runes[ix] != rChevron; ix++ {
		// advance to an rCheveron
	}

	if ix > e {
		return badPair, false
	} else if runes[ix] != rChevron {
		return badPair, false
	}

	ix++

	if ix > e {
		return badPair, false
	}

	return shFindFirstOccurenceOfElement(runes, ix, e, elem)
}

// shdebug is used in debugging to view a chunk of data as a string instead of a rune slice (i.e. so you can log it or
// view it in a debugger). index is your area of interest, window is the number of chars before and after to include.
func shdebug(runes []rune, index, window int) string {
	a := maxi(0, index-window)
	b := mini(len(runes), index+window+1)

	if a == b {
		return ""
	}

	return string(runes[a:b])
}

// shbad returns true if the index is out of range
func shbad(runes []rune, ix int) bool {
	if ix < 0 {
		return true
	}

	if ix >= len(runes) {
		return true
	}

	return false
}

func shFindNamespaceColon(runes []rune, first, last int) int {
	e := shSetLast(runes, last)
	ix := shSetFirst(runes, first)
	namespaceColonPos := -1

findNamespaceColon:
	for ; ix <= e; ix++ {
		if runes[ix] == lChevron {
			return -1
		} else if runes[ix] == rChevron {
			return -1
		} else if runes[ix] == ' ' {
			return -1
		} else if runes[ix] == '"' {
			return -1
		} else if runes[ix] == '=' {
			return -1
		} else if runes[ix] == ':' {
			namespaceColonPos = ix
			break findNamespaceColon
		}
	}

	return namespaceColonPos
}

// shIsTag returns true if the tag matches the desired element and false if it does not. specify whether it is an open
// tag or a close tag with isCloseTag. 'first' must be pointing to the first char 'inside' the tag, that is after '<'
// or '</'. returns the location of the closing '>' or -1 if the tag is not well formed or does not match elem
func shTagCompletion(runes []rune, first, last int, elem string) (location int, isSelfClosing bool) {
	e := shSetLast(runes, last)
	ix := shSetFirst(runes, first)
	var r rune
	namespaceColonPos := shFindNamespaceColon(runes, ix, e)
	if namespaceColonPos > 0 {
		ix = namespaceColonPos + 1
	}

	r = runes[ix]

	// we should be pointing at the first rune of the element name now
	//eliminated for optimization
	//if ix > e || r == '<' || r == ':' || r == ' ' || r == '>' {
	//	return -1, false
	//}

	elemRunes := []rune(elem)
	elemLen := len(elemRunes)
	for elemIx := 0; elemIx < elemLen; elemIx++ {
		if ix > e {
			return -1, false
		}
		r = runes[ix]
		if r == '>' {
			return -1, false
		} else if r == ':' {
			return -1, false
		} else if r != elemRunes[elemIx] {
			return -1, false
		}
		ix++
	}

	// proceed to close
	slashPos := -1
	for ; ix <= e; ix++ {
		r = runes[ix]
		if r == '/' {
			slashPos = ix
		} else if r == '>' {
			selfClosing := slashPos == ix-1
			return ix, selfClosing
		}
	}

	return -1, false
}

// shTagOpenFind returns the first and last indices of an element open tag with the name 'elem' (ignoring namespace).
// {-1, -1} indicates that no matching open tag was found. 'last' is the last rune that you want inspected for a closing
// tag. this is unlike slice indexing and more like traditional range indexing. enter -1 to go to the end of the runes.
func shTagOpenFind(runes []rune, first, last int, elem string) (found indexPair, lastCheckedIndex int, isSelfClosing bool) {
	e := shSetLast(runes, last)
	ix := shSetFirst(runes, first)
	var r rune
	foundFirst := -1

findOpenTag:
	for ; ix <= e; ix++ {
		r = runes[ix]
		if r == '<' {
			foundFirst = ix
			break findOpenTag
		}
	}

	ix++

	if ix > e {
		return badPair, ix, false
	}

	foundLast, isSelfClosing := shTagCompletion(runes, ix, e, elem)

	if foundLast <= ix {
		return badPair, ix, false
	}

	return indexPair{foundFirst, foundLast}, ix, isSelfClosing
}

// shTagCloseFind returns the first and last indices of an element close tag with the name 'elem' (ignoring namespace).
// {-1, -1} indicates that no matching open tag was found. If elements of the same name are nested, the nested close
// tags are skipped. 'first' must be the first rune index that is inside of the element you want to find the close for.
// 'last' is the last rune that you want inspected for a closing tag. this is unlike slice indexing and more like
// traditional range indexing. enter -1 to go to the end of the runes
// note: shTagCloseFind is not designed to work with self-closing tags
func shTagCloseFind(runes []rune, first, last int, elem string) (location indexPair, isSelfClosing bool) {
	e := shSetLast(runes, last)
	ix := shSetFirst(runes, first)
	var r rune
	foundFirst := -1

	// stop right away if the tag was self-closing
	//if ix >= 2 {
	//	lookback := string(runes[ix-2 : ix])
	//	if lookback == "/>" {
	//		// this is a self closing tag
	//		return indexPair{ix - 1, ix - 1}, true
	//	}
	//	use(lookback)
	//}

findLeftChevron:
	for ; ix <= e; ix++ {
		r = runes[ix]
		if r == '<' {
			foundFirst = ix
			break findLeftChevron
		}
	}

	ix++

	if ix > e {
		return badPair, false
	}

	r = runes[ix]

	if r != '/' {
		nestedElem, nestedElemRightChevronPos, isNestedSelfClosing := shTagNameFind(runes, ix, e)

		if len(nestedElem) == 0 || nestedElemRightChevronPos < 0 {
			return badPair, false
		}

		ix = nestedElemRightChevronPos

		if runes[ix] != rChevron {
			return badPair, false
		}

		ix++

		if !isNestedSelfClosing {
			if ix > e {
				return badPair, false
			}

			// now we are inside of a nested element
			nestedCloseLoc, _ := shTagCloseFind(runes, ix, e, nestedElem)
			//use(isNestedSelfClosing)

			if nestedCloseLoc == badPair {
				return badPair, false
			}

			ix = nestedCloseLoc.last
			ix++

			if ix > e {
				return badPair, false
			}
		}

		// if first==last it means that the nested call to shTagCloseFind above was self-closing
		//if isNestedSelfClosing {
		//	if nestedCloseLoc.first <= e {
		//		return nestedCloseLoc, isNestedSelfClosing
		//	}
		//}

		// now we have advanced beyond the nested element
		// we need to call ourself again to find the closing tag
		localFoundPair, isLocalFoundSelfClosing := shTagCloseFind(runes, ix, e, elem)
		return localFoundPair, isLocalFoundSelfClosing
	}

	ix++

	if ix > e {
		return badPair, false
	}

	for ; ix <= e && (runes[ix] == ' ' || runes[ix] == '\t' || runes[ix] == '\n'); ix++ {
		// advance past white space
	}

	if ix > e {
		return badPair, false
	}

	foundLast, isSelfClosing := shTagCompletion(runes, ix, last, elem)

	if foundLast <= ix {
		return badPair, false
	}

	return indexPair{foundFirst, foundLast}, isSelfClosing
}

// shTagFind returns the open and close locations for the desired tag 'elem' returns -1 (somewhere) if not found.
// 'last' is the last rune that you want inspected for a closing tag. this is unlike slice indexing and more like
// traditional range indexing. enter -1 to go to the end of the runes
func shTagFind(runes []rune, first, last int, elem string) (location tagLoc, isSelfClosing bool) {
	ix := shSetFirst(runes, first)
	e := shSetLast(runes, last)
	open := badPair

	for ; ix <= e && open == badPair; ix++ {
		open, ix, isSelfClosing = shTagOpenFind(runes, ix, e, elem)
		ix--
	}

	if open == badPair {
		return badTagLoc, false
	}

	if isSelfClosing {
		close := indexPair{open.last, open.last}
		return tagLoc{open, close}, isSelfClosing
	}

	close, isSelfClosing := shTagCloseFind(runes, open.last+1, last, elem)

	if close == badPair {
		return badTagLoc, false
	}

	return tagLoc{open, close}, isSelfClosing
}

// shSetLast returns a safe 'last' value for loops on 'runes'
func shSetLast(runes []rune, requestedLast int) int {
	l := len(runes)
	if requestedLast < 0 {
		return l
	} else if requestedLast > l-1 {
		return l - 1
	}
	return requestedLast
}

func shSetFirst(runes []rune, first int) int {
	if first < 0 {
		return 0
	} else if first > (len(runes) - 1) {
		return len(runes) - 1
	}
	return first
}

// shTagNameFind returns the name of an element and the position of the close '>' for that element. 'first' should be
// pointing at the first rune after '<' or '</'. if the element cannot be parsed, -1 is returned for 'lastPos'
func shTagNameFind(runes []rune, first, last int) (elem string, lastPos int, isSelfClosing bool) {
	e := shSetLast(runes, last)
	ix := shSetFirst(runes, first)
	isSelfClosing = false

	for ; ix <= e && runes[ix] == ' '; ix++ {
		// advance the index
	}

	if ix > e {
		return "", -1, false
	} else if runes[ix] == ' ' {
		ix++
	}

	if ix > e {
		return "", -1, false
	}

	namespaceColonPos := shFindNamespaceColon(runes, ix, e)
	if namespaceColonPos >= 0 {
		ix = namespaceColonPos + 1
	}

	strbuf := bytes.NewBuffer(make([]byte, 0, 20))

	for ; ix <= e && runes[ix] != ' ' && runes[ix] != '>' && runes[ix] != '=' && runes[ix] != '"' && runes[ix] != '/'; ix++ {
		strbuf.WriteRune(runes[ix])
	}

	// handled below i think
	//if runes[ix] == '/' {
	//	isSelfClosing = true
	//}

	elem = strbuf.String()

	if len(elem) == 0 {
		return "", -1, false
	}

	if ix > e {
		return "", -1, false
	}

	slashPos := -1
	for ; ix <= e && runes[ix] != '>'; ix++ {
		if runes[ix] == '/' {
			slashPos = ix
		}
	}

	if slashPos == ix-1 {
		isSelfClosing = true
	}

	if runes[ix] == '>' {
		return elem, ix, isSelfClosing
	}

	return "", -1, false
}

type attribute struct {
	name  indexPair
	value indexPair
}

var badAttribute = attribute{
	name:  badPair,
	value: badPair,
}

// first should be pointing at the first rune inside of that tag that you want to scan, *after* the element name.
// it may point at the first space after the element name or to the beginning of the name first attribute that you want
// to scan
func shFindAttributes(runes []rune, first, last int) ([]attribute, error) {
	// TODO - this function is causing excessive 'runtime newstack' in pprof go1.12
	attributes := make([]attribute, 0, 5)

	// inline this for optimization
	//ix := shSetFirst(runes, first)
	//e := shSetLast(runes, last)

	ix := first // do not check it due to optimization
	e := last   // do not check it due to optimization

	for ix <= e {
		a, err := shFindOneAttribute(runes, ix, e)

		if err != nil {
			return attributes, err
		} else if a == badAttribute {
			return attributes, nil
		}

		attributes = append(attributes, a)
		ix = a.value.last + 2 // go past the closing "
	}

	return attributes, nil
}

func shFindOneAttribute(runes []rune, first, last int) (attribute, error) {
	// Note: this is the most critical function in all profiling done so far, needs to be highly optimized
	//debug := shdebug(runes, 0, 100)
	//use(debug)

	ix := first // optimized out -> shSetFirst(runes, first)
	e := last   // optimized out -> shSetLast(runes, last)

	if ix > e {
		return badAttribute, nil
	}

	for ix <= e && (runes[ix] == ' ' || runes[ix] == '\t' || runes[ix] == '\n') {
		ix++
	}

	nameFirst := ix
	namespaceColon := shFindNamespaceColon(runes, ix, e)

	if ix > e {
		return badAttribute, nil
	}

	if namespaceColon > 0 {
		nameFirst = namespaceColon + 1
	}

	for ix <= e && runes[ix] != '=' && runes[ix] != '>' && runes[ix] != '/' && runes[ix] != ' ' {
		ix++
	}

	nameLast := ix - 1

	if runes[ix] == '>' || runes[ix] == '/' {
		return badAttribute, nil
	} else if runes[ix] != '=' {
		for ix <= e && runes[ix] != '=' && runes[ix] != '>' && runes[ix] != '/' {
			ix++
		}
		if runes[ix] == '>' || runes[ix] == '/' {
			return badAttribute, nil
		}
	}

	// optimized out
	//if runes[ix] != '=' {
	//	return badAttribute, errors.New("I think we have messed up here")
	//}

	for ix <= e && runes[ix] != '"' && runes[ix] != '>' && runes[ix] != '/' {
		ix++
	}

	// optimized out
	//if runes[ix] == '>' || runes[ix] == '/' {
	//	return badAttribute, nil
	//} else if runes[ix] != '"' {
	//	return badAttribute, errors.New("I think we have messed up here")
	//}

	ix++
	// now we are at the value
	valueFirst := ix

	for ix <= e && runes[ix] != '"' && runes[ix] != '>' && runes[ix] != '/' {
		ix++
	}

	// optimized out
	//if runes[ix] == '>' || runes[ix] == '/' {
	//	return badAttribute, nil
	//} else if runes[ix] != '"' {
	//	return badAttribute, errors.New("I think we have messed up here")
	//}

	valueLast := ix - 1

	// optimized out
	//if nameFirst > nameLast {
	//	return badAttribute, errors.New("bug: nameFirst >= nameLast")
	//} else if nameFirst > e || nameLast > e {
	//	return badAttribute, errors.New("bug: nameFirst > e || nameLast > e")
	//} else if nameLast >= valueFirst {
	//	return badAttribute, errors.New("bug: nameLast >= valueFirst")
	//} else if valueFirst > valueLast { // an empty string is ok
	//	valueFirst = -1
	//	valueLast = -1
	//} else if valueLast > e {
	//	return badAttribute, errors.New("bug: valueLast > e")
	//}

	a := attribute{
		indexPair{nameFirst, nameLast},
		indexPair{valueFirst, valueLast},
	}

	return a, nil
}
