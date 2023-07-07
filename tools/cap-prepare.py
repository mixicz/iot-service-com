"""
This is simple helper tool to generate base64 encoded protobuf capability message
for Hardwario Tower devices.

Usage:
    python3 cap-prepare.py <online> [<capability 1> [<capability 2> ...]]

Where:
    <online> is boolean value (True/False) if device is capable of online communication
    <capability> is string (typically 3 letters) of capability name

Example:
    python3 cap-prepare.py True tim met pvc pvd
"""

from pprint import pprint
import sys
import base64
# import struct
# import hashlib

# from google.protobuf import text_format

import interface.cap_pb2 as cap_pb2

def main():
    online = sys.argv[1].lower() == 'true'
    capabilities = sys.argv[2:]

    cap = cap_pb2.Cap()
    cap.onl = online
    cap.acc.extend(capabilities)
    # pprint(cap)

    cap_str = cap.SerializeToString()
    # cap_str = struct.pack('<I', len(cap_str)) + cap_str
    # cap_str = b'\x01' + cap_str
    # pprint(cap_str)

    cap_b64 = base64.b64encode(cap_str).decode('ascii')

    print(cap_b64)

    # print(text_format.MessageToString(cap))

    # print(cap_str)
    # print(cap_str.hex())

    # print(hashlib.sha256(cap_str).hexdigest())

if __name__ == '__main__':
    main()