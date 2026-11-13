module github.com/andrewkilp/CSC4330_Project6/packages/go/handlers

go 1.26.4

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/auth v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/claude v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/conversation v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/repository v0.0.0
)

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/providers v0.0.0 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/pgx/v5 v5.13.0 // indirect
	github.com/jackc/puddle/v2 v2.2.3 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/andrewkilp/CSC4330_Project6/packages/go/auth => ../auth

replace github.com/andrewkilp/CSC4330_Project6/packages/go/conversation => ../conversation

replace github.com/andrewkilp/CSC4330_Project6/packages/go/claude => ../claude

replace github.com/andrewkilp/CSC4330_Project6/packages/go/providers => ../providers

replace github.com/andrewkilp/CSC4330_Project6/packages/go/repository => ../repository
