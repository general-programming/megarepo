import argparse

from zuscale.util import build_cloud_init

parser = argparse.ArgumentParser(description="Generates a cloud-init file.")

parser.add_argument(
    "--stdout",
    help="Output the cloud-init to stdout.",
    action="store_true"
)

parser.add_argument(
    "template",
    help="Template file to use as a base.",
    type=str,
    nargs="?",
    const=1,
    default="cloud-init-template.yml"
)

parser.add_argument(
    "output",
    help="Output file to use.",
    type=str,
    nargs="?",
    const=1,
    default="cloud-init-autogen.yml"
)

if __name__ == "__main__":
    args = parser.parse_args()
    output = build_cloud_init(args.template)
    print(args)

    # Write the final result out.
    if args.stdout or args.output == "-":
        print(output)
    else:
        with open(args.output, "w") as f:
            f.write(output)
