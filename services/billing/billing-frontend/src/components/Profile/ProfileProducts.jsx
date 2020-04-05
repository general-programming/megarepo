import React from 'react';
import withStyles from '@material-ui/core/styles/withStyles';
import { connect } from 'react-redux';
import Typography from '@material-ui/core/Typography';

import ProductItem from './ProductItem';

const styles = theme => ({
    container: {
        paddingTop: theme.spacing.unit * 2,
    },
});

function ProfileProducts(props) {
    const { classes, subscriptions, subscriptionsError } = props;

    return (
        <div className={classes.container}>
            <Typography variant="h4" gutterBottom component="h2">
                Your subscriptions
            </Typography>
            {Object.keys(subscriptions).map((subID, i) => (
                <ProductItem key={subID} product={subscriptions[subID]} />
            ))}
            <p>
                {subscriptionsError}
            </p>
        </div>
    );
}

function mapStateToProps(state) {
    const { subscriptions } = state.subscriptions;
    const subscriptionsError = state.subscriptions.error;

    return {
        subscriptions,
        subscriptionsError,
    };
}


export default connect(mapStateToProps)(withStyles(styles)(ProfileProducts));
