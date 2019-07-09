package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type template struct {
	version int
	name    string
	ref     VM
}

func (t *template) toString() string {
	return t.name
}

type templateList []*template

func (tl *templateList) toString() string {
	output := "["
	for i, t := range *tl {
		if i == len(*tl)-1 {
			output += t.name
		} else {
			output += t.name + ", "
		}
	}
	output += "]"
	return output
}

type byVersion []*template

func (s byVersion) Len() int {
	return len(s)
}

func (s byVersion) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s byVersion) Less(i, j int) bool {
	return s[i].version < s[j].version
}

func getTemplate(re *regexp.Regexp, machine VM) *template {
	ver := re.FindStringSubmatch(machine.Name())
	if len(ver) <= 1 {
		return nil
	}
	t := &template{
		ref:     machine,
		name:    machine.Name(),
		version: 0,
	}

	i, err := strconv.Atoi(ver[len(ver)-1])
	if err == nil {
		t.version = i
	}
	return t
}

func matchHost(host string, rpPath string) bool {
	var hostMatchRegex, err = regexp.Compile(fmt.Sprintf("/.*/%s/Resources/.*", host))
	if err != nil {
		return false
	}
	return hostMatchRegex.Match([]byte(rpPath))
}

func parseBool(r string, def bool) bool {
	if r == "" {
		return def
	}

	switch strings.ToLower(r) {
	case "t", "y", "1", "T", "true":
		return true
	}

	return false
}
