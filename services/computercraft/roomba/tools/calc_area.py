x1 = input("x1: ")
z1 = input("z1: ")

x2 = input("x2: ")
z2 = input("z2: ")

x_center = (int(x1) + int(x2)) / 2
z_center = (int(z1) + int(z2)) / 2

print(f"Point1: {x1}, {z1}")
print(f"Point2: {x2}, {z2}")
print(f"Center: {x_center}, {z_center}")
print(f"Rounded Center: {round(x_center)}, {round(z_center)}")
