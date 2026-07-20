import socket

targets = [
    ('8.8.8.8', 80),
    ('127.0.0.1', 80),
]

for host, port in targets:
    try:
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(3)
        s.connect((host, port))
        print(f'LEAK: connected to {host}:{port}')
        s.close()
    except OSError as e:
        print(f'blocked: connect to {host}:{port} failed ({e})')
