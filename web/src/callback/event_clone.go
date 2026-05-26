/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

// Clone deep-copies Event (one-level map copy is enough).
func (e *Event) Clone() *Event {
	if e == nil {
		return nil
	}
	cp := *e

	// Deep copy tags.
	if e.Resource.Tags != nil {
		tags := make(map[string]string, len(e.Resource.Tags))
		for k, v := range e.Resource.Tags {
			tags[k] = v
		}
		cp.Resource.Tags = tags
	}

	// Deep copy data (scalar values are sufficient).
	if e.Data != nil {
		m := make(map[string]interface{}, len(e.Data))
		for k, v := range e.Data {
			m[k] = v
		}
		cp.Data = m
	}

	// Deep copy metadata (scalar values are sufficient).
	if e.Metadata != nil {
		m := make(map[string]interface{}, len(e.Metadata))
		for k, v := range e.Metadata {
			m[k] = v
		}
		cp.Metadata = m
	}

	return &cp
}
