class logout {
    constructor(base) {
        this.__base = base;
    }
    
    start() {
        return new Promise(async (resolve, reject) => {
            try {
                await this.__base.__swaggerAPI.apis.account.post_account_logout_resource();
                resolve(true);
            } catch (error) {
                reject(error);
            }
        });
    }
}

module.exports = logout;
