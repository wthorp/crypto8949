# cryto8949
Take a spreadsheet of cryptocurrency transactions and outputs an IRS form 8949

keep up this spreadsheet up to date with all transactions. it's a bit weird but i have separate vertical sections for buys (or storj salary/bonus income), trades, transfers, and sales. i don't have a section for cryptocurrency forks (which are a taxable event!) as i haven't figured out what to do about that yet

okay and then, attached is my go program that takes the above spreadsheet in CSV form and spits out a form 8949 for your taxes:

it's super gross go code. lots of panics because it started off as a small single function main method

if you enter ALL OF YOUR CRYPTOCURRENCY TRANSACTIONS EVER into it (which i have!) it will keep track of long term/short term gains/etc

the go code assumes LIFO
