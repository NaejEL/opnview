// Package testtiming records where the everyday suite's time goes, per package: each test
// database's clone, Open's schema apply and read-role grants, every wait for the database, the drop,
// and each database's life, the test's body. It records only when OPNVIEW_TEST_TIMING_DIR names a
// directory -- ci/checks.sh sets it when its CI-only switch OPNVIEW_TEST_BREAKDOWN is on, and nothing
// else does -- writing one file per test process there, which ci/testtiming sums. The shared test
// helpers call it (internal/store's test databases, internal/store/storetest); a test never does.
package testtiming
