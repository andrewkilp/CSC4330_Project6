# Shared code
Keep Go capabilities under go/ and TypeScript client code under ts/.
Go handlers own transport; future provider clients and persistence use separate packages.
Only ts/api calls the application API. Vendor credentials and vendor requests stay server-side.
