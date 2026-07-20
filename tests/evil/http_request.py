import urllib.request

try:
    urllib.request.urlopen('http://example.com', timeout=3)
    print('LEAK: outbound HTTP request succeeded')
except OSError as e:
    print(f'blocked: HTTP request failed ({e})')
