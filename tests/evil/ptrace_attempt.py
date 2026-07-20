import ctypes

PTRACE_TRACEME = 0

try:
    libc = ctypes.CDLL('libc.so.6', use_errno=True)
    ctypes.set_errno(0)
    result = libc.ptrace(PTRACE_TRACEME, 0, 0, 0)
    if result == -1:
        errno = ctypes.get_errno()
        print(f'blocked: ptrace(PTRACE_TRACEME) failed (errno={errno})')
    else:
        print(f'LEAK: ptrace(PTRACE_TRACEME) succeeded, result={result}')
except OSError as e:
    print(f'blocked: ptrace call itself failed to dispatch ({e})')
