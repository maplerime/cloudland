/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

// Clone 深拷贝 Event（只拷贝一层 map 即可）
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	cp := *e

	// Tags 深拷贝
	if e.Resource.Tags != nil {
		tags := make(map[string]string, len(e.Resource.Tags))
		for k, v := range e.Resource.Tags {
			tags[k] = v
		}
		cp.Resource.Tags = tags
	}

	// Data 深拷贝（标量足够）
	if e.Data != nil {
		m := make(map[string]interface{}, len(e.Data))
		for k, v := range e.Data {
			m[k] = v
		}
		cp.Data = m
	}

	// Metadata 深拷贝（标量足够）
	if e.Metadata != nil {
		m := make(map[string]interface{}, len(e.Metadata))
		for k, v := range e.Metadata {
			m[k] = v
		}
		cp.Metadata = m
	}

	return &cp
}
