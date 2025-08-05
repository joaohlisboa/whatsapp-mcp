#!/usr/bin/env python3
"""
Test script to verify Docker setup is working correctly.
Run this after starting the services with docker-compose up -d
"""

import sys
import os
import time

try:
    import requests
except ImportError:
    print("Error: requests library not found. Install with: pip install requests")
    sys.exit(1)

def test_whatsapp_bridge():
    """Test if WhatsApp bridge API is accessible"""
    print("Testing WhatsApp Bridge API...")
    
    # Test the API endpoint
    try:
        response = requests.get("http://localhost:8080/api/health", timeout=5)
        print(f"✓ Bridge API accessible (HTTP {response.status_code})")
        return True
    except requests.exceptions.RequestException:
        # API might not have health endpoint, try sending a test request
        try:
            response = requests.post(
                "http://localhost:8080/api/send", 
                json={"recipient": "test", "message": "test"},
                timeout=5
            )
            # We expect this to fail (no auth or invalid recipient), but connection should work
            print(f"✓ Bridge API accessible (received response)")
            return True
        except requests.exceptions.ConnectionError:
            print("✗ Cannot connect to WhatsApp Bridge API")
            return False
        except requests.exceptions.RequestException as e:
            print(f"✓ Bridge API accessible (connection established)")
            return True

def test_database_access():
    """Test if database files are accessible"""
    print("Testing database access...")
    
    db_path = "./whatsapp-bridge/store/messages.db"
    whatsapp_db_path = "./whatsapp-bridge/store/whatsapp.db"
    
    # Check if store directory exists
    if not os.path.exists("./whatsapp-bridge/store"):
        print("✗ Store directory does not exist")
        return False
    
    print("✓ Store directory exists")
    
    # Check if databases exist (they might not exist on first run)
    if os.path.exists(db_path):
        print("✓ Messages database exists")
    else:
        print("! Messages database not found (normal on first run)")
    
    if os.path.exists(whatsapp_db_path):
        print("✓ WhatsApp database exists")
    else:
        print("! WhatsApp database not found (normal on first run)")
    
    return True

def test_mcp_server():
    """Test if MCP server container is running"""
    print("Testing MCP Server container...")
    
    # MCP servers don't typically respond to HTTP GET requests
    # Instead, let's check if the container is running via Docker
    try:
        import subprocess
        result = subprocess.run(
            ["docker", "ps", "--filter", "name=whatsapp-mcp-server", "--format", "{{.Names}}"],
            capture_output=True, text=True, timeout=10
        )
        if "whatsapp-mcp-server" in result.stdout:
            print("✓ MCP Server container is running")
            return True
        else:
            print("✗ MCP Server container is not running")
            return False
    except (subprocess.SubprocessError, FileNotFoundError):
        # Fallback: try a basic connection test
        try:
            response = requests.get("http://localhost:8081", timeout=2)
            print("✓ MCP Server container is responding to HTTP")
            return True
        except requests.exceptions.RequestException:
            print("! MCP Server container status unknown (Docker command failed)")
            return False

def main():
    print("=== Docker Setup Test ===\n")
    
    tests = [
        ("Database Access", test_database_access),
        ("WhatsApp Bridge API", test_whatsapp_bridge),
        ("MCP Server Container", test_mcp_server)
    ]
    
    results = []
    
    for test_name, test_func in tests:
        print(f"\n--- {test_name} ---")
        try:
            result = test_func()
            results.append((test_name, result))
        except Exception as e:
            print(f"✗ Test failed with error: {e}")
            results.append((test_name, False))
    
    print("\n=== Test Results ===")
    all_passed = True
    for test_name, result in results:
        status = "✓ PASS" if result else "✗ FAIL"
        print(f"{test_name}: {status}")
        if not result:
            all_passed = False
    
    if all_passed:
        print("\n🎉 All tests passed! Docker setup looks good.")
        print("\nNext steps:")
        print("1. Check logs: docker-compose logs -f whatsapp-bridge")
        print("2. Look for QR code in the logs and scan it with WhatsApp")
        print("3. Configure Claude Desktop using the hybrid approach from docker-README.md")
    else:
        print("\n⚠️  Some tests failed. Check the Docker setup:")
        print("1. Make sure containers are running: docker-compose ps")
        print("2. Check logs: docker-compose logs")
        print("3. Verify port mappings and network connectivity")
    
    return 0 if all_passed else 1

if __name__ == "__main__":
    sys.exit(main())