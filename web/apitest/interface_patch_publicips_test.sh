#!/bin/bash

# Test case for PATCH /instances/{id}/interfaces/{interface_id}
# Focus: testing public_addresses field

source tokenrc

# Configuration - use fake IDs as requested
INSTANCE_ID="61400628-9a63-4fc2-91f7-76cbb4b18a53"
INTERFACE_ID="80112f4f-3440-4420-a53d-0274d2283d97"

# Public IP fake IDs for testing
PUBLIC_IP_1="7cea19e9-a1ce-490e-9505-25c9aa00c854"
PUBLIC_IP_2="6f74282c-b134-4468-8aef-00655e597a63"
PUBLIC_IP_3="024f6395-c003-4fae-8824-6181e6a110f3"

#echo "=========================================="
#echo "Test 1: Add single public IP to interface"
#echo "=========================================="
#
#cat >tmp.json <<EOF
#{
#  "public_addresses": [
#    {"id": "c4c8b6e1-ef0a-4ed4-988f-46c54bbc67cd"}
#  ]
#}
#EOF
#
#echo "Request payload:"
#cat tmp.json
#echo ""
#echo "Response:"
#curl -k -XPATCH \
#  -H "Authorization: bearer $token" \
#  -H "Content-Type: application/json" \
#  "$endpoint/api/v1/instances/$INSTANCE_ID/interfaces/$INTERFACE_ID" \
#  -d @./tmp.json | jq .
#exit 0
#
echo ""
echo "=========================================="
echo "Test 2: Add multiple public IPs to interface"
echo "=========================================="

cat >tmp.json <<EOF
{
  "public_addresses": [
    {"id": "$PUBLIC_IP_1"},
    {"id": "$PUBLIC_IP_2"},
    {"id": "$PUBLIC_IP_3"}
  ]
}
EOF
curl -k -XPATCH \
  -H "Authorization: bearer $token" \
  -H "Content-Type: application/json" \
  "$endpoint/api/v1/instances/$INSTANCE_ID/interfaces/$INTERFACE_ID" \
  -d @./tmp.json | jq .
exit 0

echo ""
echo "=========================================="
echo "Test 5: Clear all public IPs (empty array)"
echo "=========================================="

cat >tmp.json <<EOF
{
  "public_addresses": []
}
EOF

echo "Request payload:"
cat tmp.json
echo ""
echo "Response:"
curl -k -XPATCH \
  -H "Authorization: bearer $token" \
  -H "Content-Type: application/json" \
  "$endpoint/api/v1/instances/$INSTANCE_ID/interfaces/$INTERFACE_ID" \
  -d @./tmp.json | jq .

echo ""
echo "=========================================="
echo "Test 6: Verify the updated interface"
echo "=========================================="

echo "GET updated interface:"
curl -k -XGET \
  -H "Authorization: bearer $token" \
  "$endpoint/api/v1/instances/$INSTANCE_ID/interfaces/$INTERFACE_ID" | jq .

echo ""
echo "All tests completed!"
