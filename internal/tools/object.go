package tools

import (
	"encoding/json"
	"maps"
)

// object is a Doctolib JSON object whose fields are taken one by one into the
// organised result. Whatever is not taken stays, under its original name and
// nesting, in the "other" of the same level, so no field is ever lost.
type object struct {
	fields map[string]json.RawMessage
	subs   map[string]*object
}

// parseObject decodes raw as a JSON object; it returns nil when raw is anything else.
func parseObject(raw json.RawMessage) *object {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return nil
	}
	return &object{fields: fields, subs: map[string]*object{}}
}

// take decodes the field into dst and removes it. A missing field, or one
// that does not decode into dst, is left where it is and take returns false.
// dst should be a pointer to a pointer, a slice or a [json.RawMessage], so that
// null stays null.
func (o *object) take(key string, dst any) bool {
	if !o.peek(key, dst) {
		return false
	}
	delete(o.fields, key)
	return true
}

// peek decodes the field into dst without removing it.
func (o *object) peek(key string, dst any) bool {
	raw, ok := o.fields[key]
	if !ok {
		return false
	}
	return json.Unmarshal(raw, dst) == nil
}

// drop removes a field whose value was put into the organised result as it was.
func (o *object) drop(key string) {
	delete(o.fields, key)
}

// sub returns the field as an object to take fields from; what is left of it
// comes back under the same name in rest. It returns nil, leaving the field in
// place, when the field is missing or is not an object.
func (o *object) sub(key string) *object {
	child := parseObject(o.fields[key])
	if child == nil {
		return nil
	}
	delete(o.fields, key)
	o.subs[key] = child
	return child
}

// notObject is the other of an item that is not a JSON object: the item as it is.
func notObject(raw json.RawMessage) map[string]json.RawMessage {
	return map[string]json.RawMessage{"value": raw}
}

// rest is everything not taken, never nil.
func (o *object) rest() map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(o.fields)+len(o.subs))
	maps.Copy(out, o.fields)
	for k, child := range o.subs {
		left := child.rest()
		if len(left) == 0 {
			continue
		}
		raw, err := json.Marshal(left)
		if err != nil {
			continue // unreachable: the map holds valid JSON values
		}
		out[k] = raw
	}
	return out
}
