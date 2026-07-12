try:
    open('/etc/passwd').read()
    print('LEAK: read /etc/passwd')
except FileNotFoundError:
    print('blocked: /etc/passwd not found')

try:
    open('/home/ubuntu/secureCode/cmd/server/main.go').read()
    print('LEAK: read main.go')
except FileNotFoundError:
    print('blocked: main.go not found')
