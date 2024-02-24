# nuke
```
factory_reset
clear os 0
clear os 1
clear all
purgeenv
boot
```

# nuke (lighter)
```
factory_reset
clear os 0
clear os 1
```

# convert iap
```
convert-aos-ap cap <ip>
```

## for home
```
convert-aos-ap cap 10.36.75.3
```

# apboot upgrade
```
dhcp
setenv serverip <tftp>
upgrade os <filename>
```

# upgrades

## scorpio (AP-555)

### 8.5.0.14
```
setenv serverip 10.36.75.5
upgrade os ArubaInstant_Scorpio_8.5.0.14_81694
```

### 8.10.0.10
```
setenv serverip 10.36.75.5
upgrade os ArubaInstant_Scorpio_8.10.0.10_89128
```

### 10.5.0.1
```
setenv serverip 10.36.75.5
upgrade os ArubaOS_Scorpio_10.5.0.1_88128
```

### 8.11.2.1
```
setenv serverip 10.36.75.5
upgrade os ArubaInstant_Scorpio_8.11.2.1_88699
```

## draco (AP-345)
```
setenv serverip 10.36.75.5
upgrade os ArubaInstant_Draco_8.10.0.10_89128
```
