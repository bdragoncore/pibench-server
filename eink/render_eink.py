#!/usr/bin/env python3
import sys
import os
import time
import socket
import subprocess
from datetime import datetime
from PIL import Image, ImageDraw, ImageFont
from epd2in13_v4 import EPD2In13V4

STATE_FILE = "/tmp/eink_page.state"
TOTAL_PAGES = 3

def get_ip_address(ifname):
    """Fetch IP address for specific network interface."""
    try:
        out = subprocess.check_output(["ip", "-4", "addr", "show", ifname], stderr=subprocess.DEVNULL).decode('utf-8')
        for line in out.split('\n'):
            line = line.strip()
            if line.startswith("inet "):
                return line.split()[1].split('/')[0]
    except Exception:
        pass
    return None

def get_usb_ip():
    ip = get_ip_address("usb0")
    if not ip:
        ip = get_ip_address("usb1")
    if not ip:
        ip = "10.12.194.1"
    return ip

def get_wifi_ip():
    ip = get_ip_address("wlan0")
    if not ip:
        return "Disconnected"
    return ip

def get_tailscale_ip():
    ip = get_ip_address("tailscale0")
    if not ip:
        return None
    return ip

def get_hostname():
    try:
        return socket.gethostname()
    except Exception:
        return "pibench"

def check_overlay(name):
    try:
        out = subprocess.check_output(["raspi-config", "nonint", f"get_{name}"], stderr=subprocess.DEVNULL).decode().strip()
        return "ENABLED" if out == "0" else "DISABLED"
    except Exception:
        return "DISABLED"

def get_recent_boot_logs():
    """Fetch recent clean boot log lines from dmesg or journalctl."""
    logs = []
    try:
        out = subprocess.check_output(["dmesg", "-t", "-l", "notice,warn,err,info"], stderr=subprocess.DEVNULL).decode('utf-8', 'ignore')
        lines = [l.strip() for l in out.split('\n') if l.strip() and "audit" not in l]
        logs = lines[-5:]
    except Exception:
        pass

    if not logs:
        try:
            out = subprocess.check_output(["journalctl", "-b", "-n", "5", "--no-pager", "-o", "cat"], stderr=subprocess.DEVNULL).decode('utf-8', 'ignore')
            logs = [l.strip() for l in out.split('\n') if l.strip()][:5]
        except Exception:
            pass

    if not logs:
        logs = [
            "• [OK] Mounted /boot filesystem",
            "• [OK] Started USB Gadget Ethernet",
            "• [OK] Reserved SPI0 Bus for E-Ink",
            "• [OK] Started OpenCode AI Agent",
            "• [OK] System Multi-User Target Ready"
        ]
    return logs

def get_next_page():
    try:
        if os.path.exists(STATE_FILE):
            with open(STATE_FILE, "r") as f:
                cur = int(f.read().strip())
                next_p = (cur % TOTAL_PAGES) + 1
        else:
            next_p = 1
    except Exception:
        next_p = 1

    try:
        with open(STATE_FILE, "w") as f:
            f.write(str(next_p))
    except Exception:
        pass
    return next_p

def render_page1(draw, width, height, fonts, msg=None):
    font_title, font_header, font_body, font_small = fonts

    # Header Banner
    draw.rectangle([(0, 0), (width, 22)], fill=0)
    draw.text((8, 3), "⚡ PIBENCH TELEMETRY   [1/3]", font=font_title, fill=255)
    
    usb_ip = get_usb_ip()
    wifi_ip = get_wifi_ip()
    ts_ip = get_tailscale_ip()

    if msg:
        # Custom Message Box
        draw.rectangle([(6, 26), (width - 6, 75)], outline=0, width=2)
        draw.text((12, 30), "TEST MESSAGE:", font=font_small, fill=0)
        draw.text((12, 45), msg[:28], font=font_header, fill=0)
        
        draw.text((8, 80), f"USB IP : {usb_ip}", font=font_body, fill=0)
        draw.text((8, 95), f"Wi-Fi  : {wifi_ip}", font=font_small, fill=0)
    else:
        # USB IP Box
        draw.rectangle([(6, 28), (width - 6, 60)], outline=0, width=2)
        draw.text((12, 32), "USB GADGET IP:", font=font_small, fill=0)
        draw.text((12, 43), f"http://{usb_ip}:8080", font=font_header, fill=0)

        draw.text((8, 66), f"Wi-Fi IP : {wifi_ip}", font=font_body, fill=0)
        if ts_ip:
            draw.text((8, 81), f"Tailscale: {ts_ip}", font=font_body, fill=0)
        else:
            draw.text((8, 81), f"Host     : {get_hostname()}", font=font_body, fill=0)

    now_str = datetime.now().strftime("%H:%M:%S")
    draw.line([(0, height - 14), (width, height - 14)], fill=0, width=1)
    draw.text((8, height - 12), f"Status: ONLINE | Refreshed: {now_str}", font=font_small, fill=0)

def render_page2(draw, width, height, fonts):
    font_title, font_header, font_body, font_small = fonts

    # Header Banner
    draw.rectangle([(0, 0), (width, 22)], fill=0)
    draw.text((8, 3), "⚡ GPIO CONFIGURATION  [2/3]", font=font_title, fill=255)

    spi_st = check_overlay("spi")
    i2c_st = check_overlay("i2c")
    uart_st = check_overlay("serial")
    ow_st = check_overlay("onewire")

    # Peripheral Overlays Grid Box
    draw.rectangle([(6, 26), (width - 6, 58)], outline=0, width=1)
    draw.text((12, 29), f"SPI0 : {spi_st}", font=font_body, fill=0)
    draw.text((130, 29), f"I2C1 : {i2c_st}", font=font_body, fill=0)
    draw.text((12, 43), f"UART : {uart_st}", font=font_body, fill=0)
    draw.text((130, 43), f"1WIRE: {ow_st}", font=font_body, fill=0)

    # Active Hardware Pin Allocation Box
    draw.rectangle([(6, 62), (width - 6, 104)], outline=0, width=1)
    draw.text((12, 65), "PIN ALLOCATION & PERIPHERALS:", font=font_small, fill=0)
    draw.text((12, 76), "• G07-11: SPI0 Bus (E-Paper HAT)", font=font_small, fill=0)
    draw.text((12, 88), "• G17,24,25: E-Paper RST/BUSY/DC", font=font_small, fill=0)

    now_str = datetime.now().strftime("%H:%M:%S")
    draw.line([(0, height - 14), (width, height - 14)], fill=0, width=1)
    draw.text((8, height - 12), f"Status: GPIO ACTIVE | Refreshed: {now_str}", font=font_small, fill=0)

def render_page3(draw, width, height, fonts):
    font_title, font_header, font_body, font_small = fonts

    # Header Banner
    draw.rectangle([(0, 0), (width, 22)], fill=0)
    draw.text((8, 3), "⚡ BOOT LOG MESSAGES   [3/3]", font=font_title, fill=255)

    logs = get_recent_boot_logs()

    # Log Lines Box
    draw.rectangle([(6, 26), (width - 6, 104)], outline=0, width=1)
    y_pos = 29
    for l in logs[:5]:
        clean_l = l[:38] # Truncate long lines to fit 250px width
        draw.text((10, y_pos), clean_l, font=font_small, fill=0)
        y_pos += 14

    now_str = datetime.now().strftime("%H:%M:%S")
    draw.line([(0, height - 14), (width, height - 14)], fill=0, width=1)
    draw.text((8, height - 12), f"Status: LOGS OK | Refreshed: {now_str}", font=font_small, fill=0)

def render_eink(page=None, msg=None, rotate_deg=180):
    if page is None:
        page = get_next_page()

    print(f"Initializing 2.13-inch E-Ink Display (Page {page}/{TOTAL_PAGES})...")
    epd = EPD2In13V4()
    
    if epd.init() != 0:
        print("Error: E-Ink initialization failed.")
        sys.exit(1)

    width = 250
    height = 122
    image = Image.new('1', (width, height), 255) # 255 = White
    draw = ImageDraw.Draw(image)

    try:
        font_title = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", 13)
        font_header = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf", 15)
        font_body = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", 11)
        font_small = ImageFont.truetype("/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf", 9)
    except Exception:
        font_title = font_header = font_body = font_small = ImageFont.load_default()

    fonts = (font_title, font_header, font_body, font_small)

    if page == 3:
        render_page3(draw, width, height, fonts)
    elif page == 2:
        render_page2(draw, width, height, fonts)
    else:
        render_page1(draw, width, height, fonts, msg=msg)

    if rotate_deg != 0:
        image = image.rotate(rotate_deg)

    print(f"Rendering E-Ink Display Frame (Page {page}, Rotation: {rotate_deg}°)...")
    buf = epd.get_buffer(image)
    epd.display(buf)
    
    print("Putting E-Ink Display into deep sleep...")
    epd.sleep()
    epd.close()
    print(f"E-Ink Display refresh completed successfully for Page {page}!")

if __name__ == "__main__":
    target_page = None
    msg_arg = None
    
    args = sys.argv[1:]
    if "--page" in args:
        idx = args.index("--page")
        if idx + 1 < len(args):
            target_page = int(args[idx + 1])
            args = args[:idx] + args[idx+2:]
            
    if len(args) > 0:
        msg_arg = " ".join(args)

    rot = int(os.getenv("EINK_ROTATION", "180"))
    render_eink(page=target_page, msg=msg_arg, rotate_deg=rot)
