package tools

import (
	"encoding/json"
	"reflect"
)

// object keeps what isn't taken in the other of the same level, so no field is lost.
type object struct {
	fields map[string]json.RawMessage
	subs   map[string]*object
}

func parseObject(raw json.RawMessage) *object {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil
	}
	return &object{fields: fields, subs: map[string]*object{}}
}

// take leaves a field that doesn't decode into dst in place, and dst as it was. dst should be a pointer to a pointer, a
// slice or a [json.RawMessage], so that null stays null.
func (o *object) take(key string, dst any) bool {
	if !o.peek(key, dst) {
		return false
	}
	delete(o.fields, key)
	return true
}

// peek sets dst only when the whole field decodes: before it reports a type error, encoding/json has filled in what
// it could, such as a pointer to a zero value.
func (o *object) peek(key string, dst any) bool {
	raw, ok := o.fields[key]
	if !ok {
		return false
	}
	v := reflect.New(reflect.TypeOf(dst).Elem())
	if json.Unmarshal(raw, v.Interface()) != nil {
		return false
	}
	reflect.ValueOf(dst).Elem().Set(v.Elem())
	return true
}

// drop is for a field that was peeked and put into the result unchanged.
func (o *object) drop(key string) {
	delete(o.fields, key)
}

// dropEmpty drops the fields that are null, [] or {}. It doesn't look into sub-objects.
func (o *object) dropEmpty() {
	for k, v := range o.fields {
		if empty(v) {
			delete(o.fields, k)
		}
	}
}

// sub's leftovers come back in rest under the field's name. An empty object stays in place.
func (o *object) sub(key string) *object {
	child := parseObject(o.fields[key])
	if child == nil || len(child.fields) == 0 {
		return child
	}
	delete(o.fields, key)
	o.subs[key] = child
	return child
}

func notObject(raw json.RawMessage) map[string]any {
	return map[string]any{"value": raw}
}

// rest leaves out a sub-object with nothing left.
func (o *object) rest() map[string]any {
	out := make(map[string]any, len(o.fields)+len(o.subs))
	for k, v := range o.fields {
		out[k] = v
	}
	for k, child := range o.subs {
		if left := child.rest(); len(left) > 0 {
			out[k] = left
		}
	}
	return out
}
