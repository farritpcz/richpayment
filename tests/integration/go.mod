module github.com/farritpcz/richpayment/tests/integration

go 1.26.1

require (
	github.com/farritpcz/richpayment/pkg v0.0.0
	github.com/google/uuid v1.6.0
	github.com/shopspring/decimal v1.4.0
)

replace (
	github.com/farritpcz/richpayment/pkg => ../../pkg
	github.com/farritpcz/richpayment/services/commission => ../../services/commission
	github.com/farritpcz/richpayment/services/order => ../../services/order
	github.com/farritpcz/richpayment/services/parser => ../../services/parser
	github.com/farritpcz/richpayment/services/telegram => ../../services/telegram
	github.com/farritpcz/richpayment/services/wallet => ../../services/wallet
	github.com/farritpcz/richpayment/services/withdrawal => ../../services/withdrawal
)
