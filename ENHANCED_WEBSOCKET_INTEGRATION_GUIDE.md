# Enhanced WebSocket Integration Guide
## Odin Streamer Real-Time Stock Data API

### 🚀 **Overview**
The Enhanced WebSocket provides persistent connections for real-time stock market data streaming with dynamic subscription management. Supports both stock symbols and token numbers for maximum flexibility and performance.

---

## 📋 **Table of Contents**
1. [Connection Setup](#connection-setup)
2. [Authentication & Client ID](#authentication--client-id)
3. [Message Format](#message-format)
4. [Subscription Methods](#subscription-methods)
5. [Market Data Streaming](#market-data-streaming)
6. [Error Handling](#error-handling)
7. [Code Examples](#code-examples)
8. [Performance Optimization](#performance-optimization)
9. [Troubleshooting](#troubleshooting)

---

## 🔌 **Connection Setup**

### **WebSocket Endpoint**
```
ws://localhost:8080/enhanced-stream?client_id=YOUR_CLIENT_ID
```

### **Connection Parameters**
| Parameter | Required | Description |
|-----------|----------|-------------|
| `client_id` | Optional | Unique identifier for persistent connections |

### **Connection Example**
```bash
# Using wscat
wscat -c "ws://localhost:8080/enhanced-stream?client_id=trading_app_123"

# Using JavaScript
const ws = new WebSocket('ws://localhost:8080/enhanced-stream?client_id=trading_app_123');
```

---

## 🔐 **Authentication & Client ID**

### **Client ID Benefits**
- **Persistent Sessions**: Reconnect with same ID to restore subscriptions
- **Session Management**: Track individual client connections
- **Auto-reconnection**: Seamless reconnection during network issues

### **Client ID Format**
- **Auto-generated**: If not provided, server generates unique ID
- **Custom Format**: Use your own format (e.g., `user_123`, `app_v2_client_456`)
- **Persistence**: Same client_id maintains subscription state across reconnections

---

## 📨 **Message Format**

### **Request Message Structure**
```json
{
  "type": "request",
  "action": "subscribe|unsubscribe|ping|get_subscriptions|market_status",
  "stocks": ["SYMBOL1", "TOKEN1", "SYMBOL2"],
  "timestamp": 1756971590827
}
```

### **Response Message Structure**
```json
{
  "type": "subscription_response|market_data|error|welcome|pong",
  "status": "success|error|connected|alive",
  "message": "Optional message",
  "data": { /* Response data */ },
  "client_id": "your_client_id",
  "timestamp": 1756971590827
}
```

---

## 📊 **Subscription Methods**

### **1. Symbol-Based Subscription**
Subscribe using human-readable stock symbols:

```json
{
  "action": "subscribe",
  "stocks": ["SBIN", "RELIANCE", "TCS", "INFY", "HDFCBANK"]
}
```

**Response:**
```json
{
  "type": "subscription_response",
  "status": "success",
  "data": {
    "action": "subscribe",
    "subscribed_stocks": ["SBIN", "RELIANCE", "TCS", "INFY", "HDFCBANK"],
    "failed_stocks": [],
    "total_subscribed": 5
  }
}
```

### **2. Token-Based Subscription (High Performance)**
Subscribe using numeric token IDs for 650x faster performance:

```json
{
  "action": "subscribe",
  "stocks": ["3045", "2885", "11536", "1594", "1333"]
}
```

**Response:**
```json
{
  "type": "subscription_response",
  "status": "success",
  "data": {
    "action": "subscribe",
    "subscribed_stocks": ["SBIN (NSE)", "RELIANCE (NSE)", "TCS (NSE)", "INFY (NSE)", "HDFCBANK (NSE)"],
    "failed_stocks": [],
    "total_subscribed": 5
  }
}
```

### **3. Mixed Subscription**
Combine symbols and tokens in the same request:

```json
{
  "action": "subscribe",
  "stocks": ["SBIN", "2885", "TCS", "1594"]
}
```

### **4. Unsubscribe**
Remove stocks from subscription (supports both symbols and tokens):

```json
{
  "action": "unsubscribe",
  "stocks": ["SBIN", "2885"]
}
```

### **5. Get Current Subscriptions**
```json
{
  "action": "get_subscriptions"
}
```

**Response:**
```json
{
  "type": "subscriptions",
  "status": "success",
  "data": {
    "subscribed_tokens": ["3045", "2885", "11536"],
    "subscribed_symbols": ["SBIN", "RELIANCE", "TCS"],
    "total_subscribed": 3
  }
}
```

### **6. Market Status**
```json
{
  "action": "market_status"
}
```

### **7. Ping/Pong**
```json
{
  "action": "ping"
}
```

---

## 📈 **Market Data Streaming**

### **Real-Time Data Format**
```json
{
  "type": "market_data",
  "symbol": "RELIANCE",
  "token": "2885",
  "exchange": "NSE",
  "data": {
    "symbol": "RELIANCE",
    "token": "2885",
    "exchange": "NSE",
    "ltp": 1365.5,
    "open": 1371.8,
    "high": 1374.0,
    "low": 1360.3,
    "close": 1372.6,
    "prev_close": 1372.6,
    "volume": 8053013,
    "percent_change": -0.5172665015299366,
    "week_52_high": 1551.0,
    "week_52_low": 1115.55,
    "week_52_high_date": "2025-09-02",
    "week_52_low_date": "2025-09-02",
    "day_high": 1374.0,
    "day_low": 1360.3,
    "avg_volume_5d": 8053013,
    "timestamp": 1756971590198,
    "last_updated": "2025-09-04T13:09:51.651469068+05:30",
    "is_new_week_52_high": false,
    "is_new_week_52_low": false
  },
  "timestamp": 1756971591651
}
```

### **Market Hours Control**
- **Streaming Active**: Only during market hours (9:15 AM - 3:30 PM IST)
- **Market Status Updates**: Automatic notifications when market opens/closes
- **Timezone**: All times in IST (Asia/Kolkata)

---

## ⚠️ **Error Handling**

### **Common Error Responses**
```json
{
  "type": "error",
  "status": "error",
  "message": "No stocks provided for subscription",
  "client_id": "your_client_id",
  "timestamp": 1756971590827
}
```

### **Error Types**
- **Invalid Stock**: Stock symbol/token not found
- **Empty Request**: No stocks provided in subscription
- **Connection Error**: WebSocket connection issues
- **Market Closed**: Attempting operations outside market hours

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
            console.log('Connected to Odin Streamer');
        };

        this.ws.onmessage = (event) => {
            const message = JSON.parse(event.data);
            this.handleMessage(message);
        };

        this.ws.onclose = () => {
            console.log('Disconnected from Odin Streamer');
            // Auto-reconnect logic
            setTimeout(() => this.connect(), 5000);
        };

        this.ws.onerror = (error) => {
            console.error('WebSocket error:', error);
        };
    }

    handleMessage(message) {
        switch (message.type) {
            case 'welcome':
                console.log('Welcome:', message.data);
                break;
            case 'market_data':
                this.onMarketData(message);
                break;
            case 'subscription_response':
                console.log('Subscription:', message.data);
                break;
            case 'error':
                console.error('Error:', message.message);
                break;
        }
    }

    // Subscribe using symbols
    subscribeSymbols(symbols) {
        this.send({
            action: 'subscribe',
            stocks: symbols
        });
    }

    // Subscribe using tokens (high performance)
    subscribeTokens(tokens) {
        this.send({
            action: 'subscribe',
            stocks: tokens
        });
    }

    // Mixed subscription
    subscribeMixed(items) {
        this.send({
            action: 'subscribe',
            stocks: items
        });
    }

    unsubscribe(items) {
        this.send({
            action: 'unsubscribe',
            stocks: items
        });
    }

    getSubscriptions() {
        this.send({
            action: 'get_subscriptions'
        });
    }

    send(message) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify(message));
        }
    }

    onMarketData(message) {
        // Handle real-time market data
        console.log(`${message.symbol}: ₹${message.data.ltp} (${message.data.percent_change.toFixed(2)}%)`);
    }
}

// Usage
const client = new OdinStreamerClient('trading_app_123');
client.connect();

// Subscribe to stocks using symbols
client.subscribeSymbols(['SBIN', 'RELIANCE', 'TCS']);

// Subscribe using tokens for better performance
client.subscribeTokens(['3045', '2885', '11536']);

// Mixed subscription
client.subscribeMixed(['SBIN', '2885', 'TCS', '1594']);
```

### **Python Client**
```python
import asyncio
import websockets
import json
from typing import List, Union

class OdinStreamerClient:
    def __init__(self, client_id: str):
        self.client_id = client_id
        self.ws = None
        self.subscriptions = set()

    async def connect(self):
        uri = f"ws://localhost:8080/enhanced-stream?client_id={self.client_id}"
        
        try:
            self.ws = await websockets.connect(uri)
            print(f"Connected to Odin Streamer as {self.client_id}")
            
            # Start listening for messages
            await self.listen()
            
        except Exception as e:
            print(f"Connection error: {e}")

    async def listen(self):
        try:
            async for message in self.ws:
                data = json.loads(message)
                await self.handle_message(data)
        except websockets.exceptions.ConnectionClosed:
            print("Connection closed")
        except Exception as e:
            print(f"Listen error: {e}")

    async def handle_message(self, message):
        msg_type = message.get('type')
        
        if msg_type == 'welcome':
            print("Welcome:", message['data'])
        elif msg_type == 'market_data':
            await self.on_market_data(message)
        elif msg_type == 'subscription_response':
            print("Subscription:", message['data'])
        elif msg_type == 'error':
            print("Error:", message['message'])

    async def subscribe_symbols(self, symbols: List[str]):
        """Subscribe using stock symbols"""
        await self.send({
            'action': 'subscribe',
            'stocks': symbols
        })

    async def subscribe_tokens(self, tokens: List[str]):
        """Subscribe using token numbers (high performance)"""
        await self.send({
            'action': 'subscribe',
            'stocks': tokens
        })

    async def subscribe_mixed(self, items: List[str]):
        """Mixed subscription with symbols and tokens"""
        await self.send({
            'action': 'subscribe',
            'stocks': items
        })

    async def unsubscribe(self, items: List[str]):
        await self.send({
            'action': 'unsubscribe',
            'stocks': items
        })

    async def get_subscriptions(self):
        await self.send({
            'action': 'get_subscriptions'
        })

    async def send(self, message):
        if self.ws:
            await self.ws.send(json.dumps(message))

    async def on_market_data(self, message):
        """Handle real-time market data"""
        symbol = message['symbol']
        data = message['data']
        ltp = data['ltp']
        change = data['percent_change']
        
        print(f"{symbol}: ₹{ltp} ({change:.2f}%)")

# Usage
async def main():
    client = OdinStreamerClient('python_client_123')
    
    # Connect and subscribe
    await client.connect()

if __name__ == "__main__":
    asyncio.run(main())
```

### **Node.js Client**
```javascript
const WebSocket = require('ws');

class OdinStreamerClient {
    constructor(clientId) {
        this.clientId = clientId;
        this.ws = null;
    }

    connect() {
        this.ws = new WebSocket(`ws://localhost:8080/enhanced-stream?client_id=${this.clientId}`);
        
        this.ws.on('open', () => {
            console.log('Connected to Odin Streamer');
            
            // Subscribe to stocks
            this.subscribeSymbols(['SBIN', 'RELIANCE', 'TCS']);
        });

        this.ws.on('message', (data) => {
            const message = JSON.parse(data);
            this.handleMessage(message);
        });

        this.ws.on('close', () => {
            console.log('Disconnected');
            // Auto-reconnect
            setTimeout(() => this.connect(), 5000);
        });
    }

    handleMessage(message) {
        switch (message.type) {
            case 'welcome':
                console.log('Welcome:', message.data.message);
                break;
            case 'market_data':
                this.onMarketData(message);
                break;
            case 'subscription_response':
                console.log('Subscribed to:', message.data.subscribed_stocks);
                break;
        }
    }

    subscribeSymbols(symbols) {
        this.send({
            action: 'subscribe',
            stocks: symbols
        });
    }

    subscribeTokens(tokens) {
        this.send({
            action: 'subscribe',
            stocks: tokens
        });
    }

    send(message) {
        if (this.ws && this.ws.readyState === WebSocket.OPEN) {
            this.ws.send(JSON.stringify(message));
        }
    }

    onMarketData(message) {
        const { symbol, data } = message;
        console.log(`${symbol}: ₹${data.ltp} (${data.percent_change.toFixed(2)}%)`);
    }
}

// Usage
const client = new OdinStreamerClient('nodejs_client_123');
client.connect();
```

---

## ⚡ **Performance Optimization**

### **Token vs Symbol Performance**
| Method | Lookup Time | Performance | Use Case |
|--------|-------------|-------------|----------|
| **Token-based** | ~0.001ms | 650x faster | High-frequency trading, bulk subscriptions |
| **Symbol-based** | ~0.5ms | Standard | User interfaces, small subscriptions |

### **Best Practices**
1. **Use Tokens for Performance**: When subscribing to many stocks
2. **Batch Subscriptions**: Subscribe to multiple stocks in single request
3. **Persistent Connections**: Reuse same client_id for reconnections
4. **Efficient Unsubscribing**: Remove unused subscriptions to reduce bandwidth

### **Token Mapping**
Common stock tokens for quick reference:
```javascript
const POPULAR_TOKENS = {
    'SBIN': '3045',
    'RELIANCE': '2885', 
    'TCS': '11536',
    'INFY': '1594',
    'HDFCBANK': '1333',
    'BHARTIARTL': '10604',
    'ICICIBANK': '4963',
    'HINDUNILVR': '356',
    'ITC': '424',
    'KOTAKBANK': '492'
};
```

---

## 🔧 **Troubleshooting**

### **Common Issues**

#### **1. Connection Refused**
```
Error: connect ECONNREFUSED 127.0.0.1:8080
```
**Solution**: Ensure Odin Streamer is running on port 8080

#### **2. Invalid Stock Symbol/Token**
```json
{
  "data": {
    "failed_stocks": ["INVALID_SYMBOL"]
  }
}
```
**Solution**: Verify stock symbol/token exists in database

#### **3. No Data During Market Hours**
**Possible Causes**:
- Market is closed (outside 9:15 AM - 3:30 PM IST)
- No subscriptions active
- Network connectivity issues

#### **4. Connection Drops**
**Solutions**:
- Implement auto-reconnection logic
- Use same client_id to restore subscriptions
- Add connection health checks (ping/pong)

### **Debug Mode**
Enable verbose logging to troubleshoot issues:
```javascript
// Add debug logging
ws.onmessage = (event) => {
    console.log('Received:', event.data);
    const message = JSON.parse(event.data);
    // Handle message...
};
```

---

## 📚 **API Reference**

### **Actions**
| Action | Description | Parameters |
|--------|-------------|------------|
| `subscribe` | Add stocks to subscription | `stocks: string[]` |
| `unsubscribe` | Remove stocks from subscription | `stocks: string[]` |
| `get_subscriptions` | Get current subscriptions | None |
| `market_status` | Get market status | None |
| `ping` | Health check | None |

### **Message Types**
| Type | Description |
|------|-------------|
| `welcome` | Initial connection message |
| `market_data` | Real-time stock data |
| `subscription_response` | Subscription confirmation |
| `subscriptions` | Current subscription list |
| `market_status` | Market open/close status |
| `pong` | Ping response |
| `error` | Error message |

### **Market Data Fields**
| Field | Type | Description |
|-------|------|-------------|
| `ltp` | number | Last traded price |
| `open` | number | Opening price |
| `high` | number | Day high |
| `low` | number | Day low |
| `close` | number | Previous close |
| `volume` | number | Trading volume |
| `percent_change` | number | Percentage change |
| `week_52_high` | number | 52-week high |
| `week_52_low` | number | 52-week low |

---

## 🎯 **Quick Start Checklist**

- [ ] Ensure Odin Streamer is running on port 8080
- [ ] Choose unique client_id for your application
- [ ] Connect to WebSocket endpoint
- [ ] Handle welcome message
- [ ] Subscribe to desired stocks (symbols or tokens)
- [ ] Implement market data handler
- [ ] Add error handling and reconnection logic
- [ ] Test during market hours (9:15 AM - 3:30 PM IST)

---

## 📞 **Support**

For integration support or questions:
- Check server logs for detailed error messages
- Verify market hours for data streaming
- Use ping/pong for connection health checks
- Implement proper error handling and reconnection logic

---

**Happy Trading! 🚀📈**
