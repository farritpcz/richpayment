module github.com/farritpcz/richpayment/services/auth

go 1.26.1

require (
	github.com/google/uuid v1.6.0
	github.com/pquerna/otp v1.4.0
	github.com/redis/go-redis/v9 v9.18.0
	golang.org/x/crypto v0.50.0
)

require (
	github.com/boombuler/barcode v1.0.1-0.20190219062509-6c824513bacc // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/dgryski/go-rendezvous v0.0.0-20200823014737-9f7001d12a5f // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	go.uber.org/atomic v1.11.0 // indirect
)

replace github.com/farritpcz/richpayment/pkg => ../../pkg
