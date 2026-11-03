package dub

import (
	"reflect"
	"strings"
	"testing"
)

// TestCloneIsDeep: a clone shares no slice, map or pointer with the
// project. The dub service caches decoded projects and hands out clones
// (services/dub_cache.go): a shared one would let an edit of a copy
// change the cache.
func TestCloneIsDeep(t *testing.T) {
	var p Project
	fill(reflect.ValueOf(&p).Elem(), 0)
	q := p.Clone()
	var shared []string
	aliases(reflect.ValueOf(p), reflect.ValueOf(*q), "Project", &shared)
	if len(shared) > 0 {
		t.Errorf("Clone shares:\n%s", strings.Join(shared, "\n"))
	}
}

// fill gives every slice, map and pointer under v one element.
func fill(v reflect.Value, depth int) {
	if depth > 8 {
		return
	}
	switch v.Kind() {
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fill(v.Field(i), depth+1)
			}
		}
	case reflect.Pointer:
		if v.IsNil() {
			v.Set(reflect.New(v.Type().Elem()))
		}
		fill(v.Elem(), depth+1)
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 { // raw JSON, bytes
			v.Set(reflect.ValueOf([]byte("{}")).Convert(v.Type()))
			return
		}
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fill(s.Index(0), depth+1)
		v.Set(s)
	case reflect.Map:
		m := reflect.MakeMap(v.Type())
		k := reflect.New(v.Type().Key()).Elem()
		if k.Kind() == reflect.String {
			k.SetString("k")
		}
		e := reflect.New(v.Type().Elem()).Elem()
		fill(e, depth+1)
		m.SetMapIndex(k, e)
		v.Set(m)
	case reflect.String:
		v.SetString("x")
	}
}

// aliases lists the paths where a and b share memory.
func aliases(a, b reflect.Value, path string, out *[]string) {
	switch a.Kind() {
	case reflect.Struct:
		for i := range a.NumField() {
			if a.Type().Field(i).IsExported() {
				aliases(a.Field(i), b.Field(i), path+"."+a.Type().Field(i).Name, out)
			}
		}
	case reflect.Pointer:
		if a.IsNil() || b.IsNil() {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		aliases(a.Elem(), b.Elem(), path, out)
	case reflect.Slice:
		if a.Len() == 0 || b.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		for i := range min(a.Len(), b.Len()) {
			aliases(a.Index(i), b.Index(i), path+"[]", out)
		}
	case reflect.Map:
		if a.Len() == 0 || b.Len() == 0 {
			return
		}
		if a.Pointer() == b.Pointer() {
			*out = append(*out, path)
			return
		}
		for _, k := range a.MapKeys() {
			if bv := b.MapIndex(k); bv.IsValid() {
				aliases(a.MapIndex(k), bv, path+"[k]", out)
			}
		}
	}
}
