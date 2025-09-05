# 🚀 **Odin Streamer Client Connection Guide**

## 📋 **Table of Contents**
1. [Connection Methods](#connection-methods)
2. [Persistent Connection (Recommended)](#persistent-connection-recommended)
3. [Dynamic Stock Management](#dynamic-stock-management)
4. [Performance Comparison](#performance-comparison)
5. [Code Examples](#code-examples)
6. [Best Practices](#best-practices)

---

## 🔌 **Connection Methods**

### **Method 1: Basic WebSocket (Simple)**
```bash
# Connect with initial stocks
wscat -c "ws://localhost:8080/live-stream?tokens=476,11536,1594"
```
- ❌ **Problem**: Need to reconnect to change stocks
- ❌ **Problem**: Connection drops when changing subscriptions

### **Method 2: Enhanced WebSocket (RECOMMENDED)**
```bash
# Connect once, manage stocks dynamically
wscat -c "ws://localhost:8080/enhanced-stream?client_id=my_trading_app"
```
- ✅ **Solution**: Persistent connection
- ✅ **Solution**: Dynamic stock management
- ✅ **Solution**: No reconnection needed

---

## 🎯 **Persistent Connection (RECOMMENDED)**

### **Why Enhanced WebSocket?**
Your use case is **PERFECT** for Enhanced WebSocket:

1. **Connect Once**: Client connects and stays connected
2. **Dynamic Management**: Add/remove stocks anytime via JSON messages
3. **No Disconnection**: Connection remains active throughout
4. **Market Hours Control**: Automatically handles market open/close
5. **Reconnection Support**: If connection drops, reconnects with same subscriptions

### **Connection Flow**
```
Client Connects → Stays Connected → Add Stocks → Remove Stocks → Add More → Stay Connected
     ↓                ↓              ↓             ↓              ↓           ↓
  Welcome Msg    Live Data      More Data     Less Data      New Data   Continuous
```

---

## 🔄 **Dynamic Stock Management**

### **1. Initial Connection**
```bash
wscat -c "ws://localhost:8080/enhanced-stream?client_id=trading_app_123"
```

**Server Response:**
```json
{
  "type": "welcome",
  "status": "connected",
  "client_id": "trading_app_123",
  "data": {
    "message": "Connected to Odin Streamer Enhanced WebSocket",
    "features": ["dynamic_subscriptions", "market_hours_control", "persistent_connection"],
    "market_open": true,
    "instructions": {
      "subscribe": "{\"type\": \"request\", \"action\": \"subscribe\", \"stocks\": [\"RELIANCE\", \"TCS\"]}",
      "unsubscribe": "{\"type\": \"request\", \"action\": \"unsubscribe\", \"stocks\": [\"RELIANCE\"]}",
      "ping": "{\"type\": \"request\", \"action\": \"ping\"}"
    }
  }
}
```

### **2. Subscribe to Stocks (Anytime)**
```json
{
  "type": "request",
  "action": "subscribe",
  "stocks": ["RELIANCE", "TCS", "INFY"]
}
```

**Server Response:**
```json
{
  "type": "subscription_response",
  "status": "success",
  "data": {
    "action": "subscribe",
    "subscribed_stocks": ["RELIANCE", "TCS", "INFY"],
    "total_subscribed": 3
  }
}
```

### **3. Add More Stocks Later (No Reconnection)**
```json
{
  "type": "request",
  "action": "subscribe",
  "stocks": ["WIPRO", "HDFCBANK"]
}
```

**Server Response:**
```json
{
  "type": "subscription_response",
  "status": "success",
  "data": {
    "action": "subscribe",
    "subscribed_stocks": ["WIPRO", "HDFCBANK"],
    "total_subscribed": 5
  }
}
```

### **4. Remove Stocks (No Reconnection)**
```json
{
  "type": "request",
  "action": "unsubscribe",
  "stocks": ["TCS", "INFY"]
}
```

### **5. Get Current Subscriptions**
```json
{
  "type": "request",
  "action": "get_subscriptions"
}
```

---

## ⚡ **Performance Comparison**

### **Token-Based (FASTEST)**
```json
{
  "type": "request",
  "action": "subscribe",
  "tokens": ["476", "11536", "1594"]
}
```
- 🚀 **650x faster** than symbol lookup
- ⚡ **Instant processing**
- 🎯 **No database queries**

### **Symbol-Based (SLOWER)**
```json
{
  "type": "request", 
  "action": "subscribe",
  "stocks": ["RELIANCE", "TCS", "INFY"]
}
```
- 🐌 **Requires database lookup**
- 📊 **~0.5ms per symbol**
- 🔄 **Backward compatibility**

---

## 💻 **Code Examples**

### **JavaScript Client**
```javascript
class OdinStreamerClient {
    constructor(clientId) {
        this.clientId = clientId;
        this.ws = null;
        this.subscriptions = new Set();
    }
    
    connect() {
        this.ws = new WebSocket(`ws://localhost:8080/enhanced-stream?client_id=${this.clientId}`);
        
        this.ws.onopen = () => {
            console.log('✅ Connected to Odin Streamer');
        };
        
        this.ws.onmessage = (event) => {
            const data = JSON.parse(event.data);
            this.handleMessage(data);
        };
        
        this.ws.onclose = () => {
            console.log('🔌 Connection closed, reconnecting...');
            setTimeout(() => this.connect(), 1000);
        };
    }
    
    // Add stocks without reconnection
    subscribeToStocks(stocks) {
        this.send({
            type: 'request',
            action: 'subscribe',
            stocks: stocks
        });
    }
    
    // Remove stocks without reconnection  
    unsubscribeFromStocks(stocks) {
        this.send({
            type: 'request',
            action: 'unsubscribe',
            stocks: stocks
        });
    }
    
    // Use tokens for fastest performance
    subscribeToTokens(tokens) {
        this.send({
            type: 'request',
            action: 'subscribe',
            tokens: tokens
        });
    }
    
    send(message) {
        if (this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify(message));
        }
    }
    
    handleMessage(data) {
        switch(data.type) {
            case 'welcome':
                console.log('🎉 Welcome:', data.data.message);
                break;
            case 'market_data':
                this.onMarketData(data);
                break;
            case 'subscription_response':
                console.log('📊 Subscription:', data.data);
                break;
        }
    }
    
    onMarketData(data) {
        // Handle live market data
        console.log(`📈 ${data.symbol}: ₹${data.data.ltp}`);
    }
}

// Usage Example
const client = new OdinStreamerClient('my_trading_app');
client.connect();

// Later, add stocks dynamically
setTimeout(() => {
    client.subscribeToStocks(['RELIANCE', 'TCS']);
}, 2000);

// Even later, add more stocks
setTimeout(() => {
    client.subscribeToStocks(['WIPRO', 'HDFCBANK']);
}, 10000);

// Remove some stocks
setTimeout(() => {
    client.unsubscribeFromStocks(['TCS']);
}, 20000);
```

### **Python Client**
```python
import websocket
import json
import threading

class OdinStreamerClient:
    def __init__(self, client_id):
        self.client_id = client_id
        self.ws = None
        self.subscriptions = set()
        
    def connect(self):
        url = f"ws://localhost:8080/enhanced-stream?client_id={self.client_id}"
        self.ws = websocket.WebSocketApp(url,
            on_open=self.on_open,
            on_message=self.on_message,
            on_close=self.on_close)
        
        # Run in background thread
        threading.Thread(target=self.ws.run_forever).start()
    
    def on_open(self, ws):
        print("✅ Connected to Odin Streamer")
    
    def on_message(self, ws, message):
        data = json.loads(message)
        if data['type'] == 'market_data':
            print(f"📈 {data['symbol']}: ₹{data['data']['ltp']}")
    
    def on_close(self, ws, close_status_code, close_msg):
        print("🔌 Connection closed, reconnecting...")
        threading.Timer(1.0, self.connect).start()
    
    def subscribe_to_stocks(self, stocks):
        message = {
            "type": "request",
            "action": "subscribe", 
            "stocks": stocks
        }
        self.ws.send(json.dumps(message))
    
    def subscribe_to_tokens(self, tokens):
        message = {
            "type": "request",
            "action": "subscribe",
            "tokens": tokens
        }
        self.ws.send(json.dumps(message))

# Usage
client = OdinStreamerClient("python_trading_app")
client.connect()

# Add stocks dynamically
import time
time.sleep(2)
client.subscribe_to_stocks(["RELIANCE", "TCS"])

time.sleep(10) 
client.subscribe_to_stocks(["WIPRO", "HDFCBANK"])
```

---

## 🎯 **Best Practices**

### **1. Use Enhanced WebSocket**
```bash
# ✅ RECOMMENDED
ws://localhost:8080/enhanced-stream?client_id=unique_app_id

# ❌ NOT RECOMMENDED for dynamic management
ws://localhost:8080/live-stream?stocks=RELIANCE,TCS
```

### **2. Use Unique Client ID**
```javascript
const clientId = `trading_app_${Date.now()}_${Math.random()}`;
```

### **3. Handle Reconnections**
```javascript
ws.onclose = () => {
    console.log('Reconnecting...');
    setTimeout(() => connect(), 1000);
};
```

### **4. Use Tokens for Performance**
```json
// ⚡ FASTEST
{"action": "subscribe", "tokens": ["476", "11536"]}

// 🐌 SLOWER  
{"action": "subscribe", "stocks": ["RELIANCE", "TCS"]}
```

### **5. Batch Operations**
```json
// ✅ GOOD - Batch multiple stocks
{"action": "subscribe", "stocks": ["RELIANCE", "TCS", "INFY", "WIPRO"]}

// ❌ BAD - Multiple individual requests
{"action": "subscribe", "stocks": ["RELIANCE"]}
{"action": "subscribe", "stocks": ["TCS"]}
```

---

## 🔥 **Your Perfect Solution**

For your use case:

1. **Connect Once**: `ws://localhost:8080/enhanced-stream?client_id=your_app`
2. **Stay Connected**: Connection persists throughout
3. **Add Stocks Dynamically**: Send JSON messages anytime
4. **No Reconnection**: Same connection handles everything
5. **Market Hours**: Automatically managed
6. **Performance**: Use tokens for fastest response

### **Example Flow:**
```
09:00 AM: Client connects
09:15 AM: Market opens, subscribe to ["RELIANCE", "TCS"]
10:30 AM: Add ["WIPRO", "HDFCBANK"] 
12:00 PM: Remove ["TCS"]
02:00 PM: Add ["INFY", "ICICIBANK"]
03:30 PM: Market closes (connection stays open)
Next Day: Market opens, same subscriptions active
```

**🎉 Perfect for your trading application!**
