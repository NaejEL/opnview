cd /workspace && go test -c -o /tmp/f.test ./internal/front/frontjs/ || exit 1
cd /workspace/internal/front/frontjs
run() { ( time "$@" ) 2>&1 | grep -E "real|user"; }
for g in 100 400; do echo "GOGC=$g 4 cpus"; GOGC=$g run taskset -c 0-3 /tmp/f.test -test.count=1 -test.skip ZZ; done
echo "GOGC=400 2 cpus"; GOGC=400 run taskset -c 0-1 /tmp/f.test -test.count=1 -test.skip ZZ
GOGC=400 /tmp/f.test -test.count=1 -test.skip ZZ -test.memprofile /tmp/mem.out >/dev/null; go tool pprof -sample_index=alloc_space -top /tmp/f.test /tmp/mem.out 2>/dev/null | sed -n 4,6p
