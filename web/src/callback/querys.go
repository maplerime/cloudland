/*
Copyright <holder> All Rights Reserved.
SPDX-License-Identifier: Apache-2.0
*/

package callback

const sqlSelectInstanceByID = `
SELECT i.id, i.uuid, i.owner,
       i.hostname, i.status, i.hyper, i.zone_id, i.cpu, i.memory, i.disk,
       o.uuid AS tenant_uuid
FROM instances i
LEFT JOIN organizations o ON o.id = i.owner
WHERE i.id = ?
LIMIT 1
`

const sqlSelectVolumeByID = `
	SELECT v.id, v.uuid, v.owner, v.name, v.status, v.size,
			v.instance_id, v.target, v.format, v.path,
			o.uuid AS tenant_uuid
	FROM volumes v
	LEFT JOIN organizations o ON o.id = v.owner
	WHERE v.id = ?
`

const sqlSelectImageByID = `
SELECT img.id, img.uuid, img.owner,
       img.name, img.status, img.format, img.os_code, img.size, img.architecture,
       o.uuid AS tenant_uuid
FROM images img
LEFT JOIN organizations o ON o.id = img.owner
WHERE img.id = ?
LIMIT 1
`

const sqlSelectInterfaceByID = `
SELECT n.id, n.uuid, n.owner,
		n.name, n.mac_addr, n.instance, n.hyper, n.type,
		o.uuid AS tenant_uuid
FROM interfaces n
LEFT JOIN organizations o ON o.id = n.owner
WHERE n.instance = ? AND n.mac_addr = ?
LIMIT 1 
`
