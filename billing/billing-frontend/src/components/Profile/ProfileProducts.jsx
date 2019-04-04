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
    const { classes, subscriptions } = props;

    return (
        <div className={classes.container}>
            <Typography variant="h4" gutterBottom component="h2">
                Your subscriptions
            </Typography>
            {Object.keys(subscriptions).map((subID, i) => (
                <ProductItem key={subID} product={subscriptions[subID]} />
            ))}
        </div>
    );
}

function mapStateToProps(state) {
    const { subscriptions } = state.subscriptions;

    return {
        subscriptions,
    };
}


export default connect(mapStateToProps)(withStyles(styles)(ProfileProducts));
