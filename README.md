# how to start

## clone repository using git and enter it
```terminal
git clone https://github.com/shrechochek/tg_devices_manager
cd tg_devices_manager
```
# how to run one bot
## create variables
### macOS
```terminal
export TELEGRAM_BOT_TOKEN="..."
export ALLOWED_USER_ID="..."  
export PASS_PHRASE="..."
```
### windows
```terminal
set TELEGRAM_BOT_TOKEN= ...
set ALLOWED_USER_ID= ...
set PASS_PHRASE= ...
```
## and run it 
### macOS
```terminal
.\tg_terminal
```
### windows
```terminal
.\tg_terminal.exe
```

# how to run multiple bots (only macos now)
## create variables for manager
### macOS
```terminal
export TELEGRAM_BOT_TOKEN="..."
export ALLOWED_USER_ID="..."
export PASS_PHRASE="..."
export SHARED_SECRET="..." #not necessary
./manager
```

## open terminal on other computers and create variables for agents
### macOS
```terminal
export MANAGER_URL="http://<manager-ip>:8080"
export DEVICE_ID="macbook-01" #example
export DEVICE_NAME="My-Mac" #example
export SHARED_SECRET="..." #not necessary
./agent
```




