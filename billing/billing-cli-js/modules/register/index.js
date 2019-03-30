const inquirer = require("inquirer");

class register {
    constructor(base) {
        this.__base = base;
    }

    askForPassword() {
        return new Promise(async (resolve, reject) => {
            try {
                let answersTwo = await inquirer.prompt([
                    {
                        type: "password",
                        name: "password",
                        mask: "*",
                        message: "Please enter your password"
                    },
                    {
                        type: "password",
                        name: "password_verify",
                        mask: "*",
                        message: "Please enter your password again"
                    }
                ]);
                if (answersTwo.password !== answersTwo.password_verify) {
                    console.error("\nPasswords don't match. Please try again.\n");
                    let pw = await this.askForPassword();
                    resolve(pw);
                } else {
                    resolve(answersTwo.password);
                }
            } catch (error) {
                reject(error);
            }
        });
    }

    start() {
        return new Promise(async (resolve, reject) => {
            try {
                let answersOne = await inquirer.prompt([
                    {
                        type: "input",
                        name: "username",
                        message: "Please enter your username",
                        filter: (val) => {
                            return val.replace(/ /g, "");
                        }
                    },
                    {
                        type: "input",
                        name: "email",
                        message: "Please enter your email address",
                        validate: (val) => {
                            // eslint-disable-next-line no-useless-escape
                            if (val.match(/^(([^<>()\[\]\\.,;:\s@"]+(\.[^<>()\[\]\\.,;:\s@"]+)*)|(".+"))@((\[[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}])|(([a-zA-Z\-0-9]+\.)+[a-zA-Z]{2,}))$/)) {
                                return true;
                            } else {
                                return "Please enter a valid email address.";
                            }
                        }
                    }
                ]);
                let password = await this.askForPassword();

                let resp = await this.__base.__swaggerAPI.apis.account.post_account_register_resource({username: answersOne.username, email: answersOne.email, password: password, password_verify: password});
                resolve(resp.obj);
            } catch (error) {
                reject(error);
            }
            
        });
    }
}

module.exports = register;
