from setuptools import setup, find_packages

setup(
    name="cloudland-cli",
    version="0.1.0",
    description="CloudLand IaaS operations CLI toolset",
    packages=find_packages(),
    python_requires=">=3.8",
    install_requires=[
        "click>=8.0",
        "requests>=2.28",
        "psycopg2-binary>=2.9",
        "rich>=13.0",
        "tomli>=2.0",
    ],
    entry_points={
        "console_scripts": [
            "cloudland=cloudland_cli.cli:cli",
        ],
    },
)
