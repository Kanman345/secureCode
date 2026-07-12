import os

pids = sorted(int(p) for p in os.listdir('/proc') if p.isdigit())
print('visible pids:', pids)
