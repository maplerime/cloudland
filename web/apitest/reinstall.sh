#!/bin/bash -xv

source tokenrc

cat >tmp.json <<EOF
{
  "keys": [
    {
      "id": "59dd901d-ac7d-4918-afbf-ff485de31f07"
    }
  ],
  "password": "Wish-Y0u-Happy"
}
EOF
instance_id=7cd4b49f-0532-4bf2-ab9f-89ddd15db4a4
curl -k -XPOST -H "Authorization: bearer $token" "$endpoint/api/v1/instances/$instance_id/reinstall" -d @./tmp.json
