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

import interface.cap_pb2 as cap_pb2

def main():
    online = sys.argv[1].lower() == 'true'
    capabilities = [s.upper() for s in sys.argv[2:]]

    cap = cap_pb2.Cap()
    cap.onl = online
    cap.acc.extend(capabilities)

    cap_str = cap.SerializeToString()

    cap_b64 = base64.b64encode(cap_str).decode('ascii')

    print(cap_b64)


if __name__ == '__main__':
    main()