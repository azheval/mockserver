package matcher

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type xmlNode struct {
	Name     string
	Attrs    map[string]string
	Text     strings.Builder
	Children []*xmlNode
	Parent   *xmlNode
}

// XMLPathValue implements a documented XPath subset: absolute child paths,
// descendant paths (//name), 1-based element indexes, attributes and text().
// Namespace prefixes are preserved as written; full XPath functions/predicates
// are intentionally not evaluated.
func XMLPathValue(data []byte, path string) (string, bool, error) {
	dec := xml.NewDecoder(strings.NewReader(string(data)))
	root := &xmlNode{Name: "#document", Attrs: map[string]string{}}
	stack := []*xmlNode{root}
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			n := &xmlNode{Name: t.Name.Local, Attrs: map[string]string{}, Parent: stack[len(stack)-1]}
			for _, a := range t.Attr {
				n.Attrs[a.Name.Local] = a.Value
			}
			n.Parent.Children = append(n.Parent.Children, n)
			stack = append(stack, n)
		case xml.CharData:
			stack[len(stack)-1].Text.Write([]byte(t))
		case xml.EndElement:
			if len(stack) > 1 {
				stack = stack[:len(stack)-1]
			}
		}
	}
	p := strings.TrimSpace(path)
	if p == "" || p == "/" {
		return "", false, fmt.Errorf("empty XPath")
	}
	desc := strings.HasPrefix(p, "//")
	p = strings.TrimLeft(p, "/")
	segments := strings.Split(p, "/")
	if len(segments) == 0 {
		return "", false, fmt.Errorf("invalid XPath")
	}
	current := []*xmlNode{root}
	for i, seg := range segments {
		if seg == "" {
			return "", false, fmt.Errorf("unsupported XPath syntax")
		}
		if seg == "text()" || strings.HasPrefix(seg, "@") {
			if i != len(segments)-1 {
				return "", false, fmt.Errorf("text()/attribute must be final XPath segment")
			}
			var values []string
			for _, n := range current {
				if seg == "text()" {
					values = append(values, strings.TrimSpace(n.Text.String()))
				} else if v, ok := n.Attrs[strings.TrimPrefix(seg, "@")]; ok {
					values = append(values, v)
				}
			}
			if len(values) == 0 {
				return "", false, nil
			}
			return strings.Join(values, ","), true, nil
		}
		name, index := parseXPathIndex(seg)
		var next []*xmlNode
		if i == 0 && !desc && root.Children != nil && root.Children[0].Name == name {
			next = append(next, root.Children[0])
		} else {
			for _, n := range current {
				var found []*xmlNode
				if desc && i == 0 {
					collectXMLDescendants(n, name, &found)
				} else {
					for _, ch := range n.Children {
						if name == "*" || ch.Name == name {
							found = append(found, ch)
						}
					}
				}
				if index > 0 {
					if index <= len(found) {
						next = append(next, found[index-1])
					}
				} else {
					next = append(next, found...)
				}
			}
		}
		if len(next) == 0 {
			return "", false, nil
		}
		current = next
	}
	var values []string
	for _, n := range current {
		values = append(values, strings.TrimSpace(n.Text.String()))
	}
	return strings.Join(values, ","), len(values) > 0, nil
}

func parseXPathIndex(segment string) (string, int) {
	if !strings.HasSuffix(segment, "]") {
		return segment, 0
	}
	i := strings.LastIndex(segment, "[")
	if i < 0 {
		return segment, 0
	}
	n, err := strconv.Atoi(segment[i+1 : len(segment)-1])
	if err != nil || n < 1 {
		return segment, 0
	}
	return segment[:i], n
}
func collectXMLDescendants(n *xmlNode, name string, out *[]*xmlNode) {
	for _, ch := range n.Children {
		if name == "*" || ch.Name == name {
			*out = append(*out, ch)
		}
		collectXMLDescendants(ch, name, out)
	}
}
