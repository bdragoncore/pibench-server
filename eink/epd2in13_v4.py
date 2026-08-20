import os
import time
import spidev
import gpiod
from gpiod.line import Direction, Value

# Waveshare / LAFVIN 2.13-inch e-Paper HAT V4 (SSD1680 controller, 250x122 resolution)
EPD_WIDTH = 122
EPD_HEIGHT = 250

# GPIO Pin Assignment (BCM Pin Numbers for 40-pin RPi header)
RST_PIN = 17  # Pin 11
DC_PIN = 25   # Pin 22
BUSY_PIN = 24 # Pin 18

class EPD2In13V4:
    def __init__(self, bus=0, device=0):
        self.width = EPD_WIDTH
        self.height = EPD_HEIGHT
        self.bytes_per_line = (EPD_WIDTH + 7) // 8 # 16 bytes per line
        
        # SPI Init
        self.spi = spidev.SpiDev()
        self.spi.open(bus, device)
        self.spi.max_speed_hz = 2000000
        self.spi.mode = 0b00

        self.lines = None
        # GPIO Init via gpiod v2 with sysfs fallback
        try:
            self.lines = gpiod.request_lines(
                "/dev/gpiochip0",
                consumer="epd2in13",
                config={
                    RST_PIN: gpiod.LineSettings(direction=Direction.OUTPUT, output_value=Value.ACTIVE),
                    DC_PIN:  gpiod.LineSettings(direction=Direction.OUTPUT, output_value=Value.INACTIVE),
                    BUSY_PIN: gpiod.LineSettings(direction=Direction.INPUT),
                }
            )
        except Exception:
            self.setup_sysfs_gpio()

    def setup_sysfs_gpio(self):
        for p, direction in [(RST_PIN, "out"), (DC_PIN, "out"), (BUSY_PIN, "in")]:
            p_dir = f"/sys/class/gpio/gpio{p}"
            if not os.path.exists(p_dir):
                try:
                    with open("/sys/class/gpio/export", "w") as f:
                        f.write(str(p))
                except Exception:
                    pass
            try:
                with open(f"{p_dir}/direction", "w") as f:
                    f.write(direction)
            except Exception:
                pass

    def digital_write(self, pin, val):
        if self.lines:
            v = Value.ACTIVE if val else Value.INACTIVE
            self.lines.set_value(pin, v)
        else:
            try:
                with open(f"/sys/class/gpio/gpio{pin}/value", "w") as f:
                    f.write("1" if val else "0")
            except Exception:
                pass

    def digital_read(self, pin):
        if self.lines:
            res = self.lines.get_value(pin)
            return 1 if res == Value.ACTIVE else 0
        else:
            try:
                with open(f"/sys/class/gpio/gpio{pin}/value", "r") as f:
                    return int(f.read().strip())
            except Exception:
                return 0

    def reset(self):
        self.digital_write(RST_PIN, 1)
        time.sleep(0.05)
        self.digital_write(RST_PIN, 0)
        time.sleep(0.05)
        self.digital_write(RST_PIN, 1)
        time.sleep(0.1)

    def send_command(self, command):
        self.digital_write(DC_PIN, 0)
        self.spi.xfer2([command])

    def send_data(self, data):
        self.digital_write(DC_PIN, 1)
        if isinstance(data, list):
            self.spi.xfer2(data)
        else:
            self.spi.xfer2([data])

    def wait_until_idle(self):
        time.sleep(0.05) # Give hardware time to activate BUSY pin
        while self.digital_read(BUSY_PIN) == 1: # 1 == Value.ACTIVE (Busy)
            time.sleep(0.02)

    def init(self):
        self.reset()
        self.wait_until_idle()

        self.send_command(0x12) # SWRESET
        self.wait_until_idle()

        self.send_command(0x01) # Driver output control
        self.send_data([0xF9, 0x00, 0x00])

        self.send_command(0x11) # Data entry mode
        self.send_data([0x03])

        self.send_command(0x44) # Set RAM X address
        self.send_data([0x00, 0x0F]) # (122 + 7)//8 - 1 = 15 = 0x0F

        self.send_command(0x45) # Set RAM Y address
        self.send_data([0x00, 0x00, 0xF9, 0x00]) # 250 - 1 = 249 = 0xF9

        self.send_command(0x3C) # Border Waveform
        self.send_data([0x05])

        self.send_command(0x21) # Display update control 1
        self.send_data([0x00, 0x80])

        self.send_command(0x4E) # Set RAM X address counter
        self.send_data([0x00])

        self.send_command(0x4F) # Set RAM Y address counter
        self.send_data([0x00, 0x00])

        self.wait_until_idle()
        return 0

    def get_buffer(self, image):
        # 16 bytes per row * 250 rows = 4000 bytes
        buf = [0xFF] * (self.bytes_per_line * self.height)
        image_monocolor = image.convert('1')
        imwidth, imheight = image_monocolor.size
        pixels = image_monocolor.load()

        if imwidth == self.width and imheight == self.height:
            # Portrait (122, 250)
            for y in range(imheight):
                for x in range(imwidth):
                    if pixels[x, y] == 0: # Black pixel
                        buf[y * self.bytes_per_line + (x // 8)] &= ~(0x80 >> (x % 8))
        elif imwidth == self.height and imheight == self.width:
            # Landscape (250, 122) -> Rotate into E-Paper RAM (122, 250)
            for y in range(imheight):
                for x in range(imwidth):
                    new_x = y
                    new_y = self.height - 1 - x
                    if pixels[x, y] == 0: # Black pixel
                        buf[new_y * self.bytes_per_line + (new_x // 8)] &= ~(0x80 >> (new_x % 8))
        return buf

    def display(self, image_buffer):
        self.send_command(0x4E) # Set RAM X address counter
        self.send_data([0x00])

        self.send_command(0x4F) # Set RAM Y address counter
        self.send_data([0x00, 0x00])

        self.send_command(0x24) # Write RAM (BW)
        for i in range(len(image_buffer)):
            self.send_data(image_buffer[i])

        self.send_command(0x22) # Display Update Control 2
        self.send_data([0xF7])

        self.send_command(0x20) # Master Activate
        self.wait_until_idle()

    def sleep(self):
        self.send_command(0x10) # Deep sleep mode
        self.send_data([0x01])
        time.sleep(0.1)

    def close(self):
        self.spi.close()
        if self.lines:
            self.lines.release()
