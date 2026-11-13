module github.com/andrewkilp/CSC4330_Project6/packages/go/handlers

go 1.26.4

require (
	github.com/andrewkilp/CSC4330_Project6/packages/go/claude v0.0.0
	github.com/andrewkilp/CSC4330_Project6/packages/go/conversation v0.0.0
)

require github.com/andrewkilp/CSC4330_Project6/packages/go/providers v0.0.0 // indirect

replace github.com/andrewkilp/CSC4330_Project6/packages/go/conversation => ../conversation

replace github.com/andrewkilp/CSC4330_Project6/packages/go/claude => ../claude

replace github.com/andrewkilp/CSC4330_Project6/packages/go/providers => ../providers
