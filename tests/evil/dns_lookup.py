import socket

try:
    ip = socket.gethostbyname('google.com')
    print(f'LEAK: resolved google.com to {ip}')
except OSError as e:
    print(f'blocked: DNS resolution failed ({e})')
