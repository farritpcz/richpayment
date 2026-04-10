module github.com/farritpcz/richpayment/services/user

go 1.26.1

replace github.com/farritpcz/richpayment/pkg => ../../pkg

require (
	github.com/farritpcz/richpayment/pkg v0.0.0-00010101000000-000000000000
	github.com/google/uuid v1.6.0
	github.com/shopspring/decimal v1.4.0
	golang.org/x/crypto v0.50.0
)
